// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { SafeERC20 } from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import { Math } from "@openzeppelin/contracts/utils/math/Math.sol";

/// @title SafeRollupGateway
/// @notice The only L1 contract of a Safe rollup besides the Safe and its withdrawal module. It
///         holds no funds: deposits are forwarded to the Safe in the same call.
///         - op-node reads it as the deposit contract (TransactionDeposited) and as the system
///           config contract (ConfigUpdate).
///         - The unchanged L2 predeploys see it as both the L1 messenger and the L1 standard bridge.
///         - The Safe calls it to relay L2 to L1 messages, so L1 targets can read the L2 sender, and
///           to deposit transactions from any L2 account.
contract SafeRollupGateway {
    using SafeERC20 for IERC20;

    /// @notice Deposit gas market, with the default OP stack ResourceConfig values.
    uint64 internal constant MAX_RESOURCE_LIMIT = 20_000_000;
    int256 internal constant TARGET_RESOURCE_LIMIT = 2_000_000;
    int256 internal constant BASE_FEE_MAX_CHANGE_DENOMINATOR = 8;
    int256 internal constant MINIMUM_BASE_FEE = 1 gwei;

    address internal constant L2_CROSS_DOMAIN_MESSENGER = 0x4200000000000000000000000000000000000007;
    address internal constant L2_STANDARD_BRIDGE = 0x4200000000000000000000000000000000000010;

    /// @notice The Safe that holds all bridged funds and is the only admin.
    address public immutable safe;

    uint128 public prevBaseFee = 1 gwei;
    uint64 public prevBoughtGas;
    uint64 public prevBlockNum = uint64(block.number);

    /// @notice Nonce of L1 to L2 messages, so that identical messages get distinct hashes on L2.
    uint240 public messageNonce;

    /// @notice L2 sender of the L2 to L1 message being relayed, zero outside of a relay.
    address public xDomainMessageSender;

    event TransactionDeposited(address indexed from, address indexed to, uint256 indexed version, bytes opaqueData);
    event ConfigUpdate(uint256 indexed version, uint8 indexed updateType, bytes data);

    error Unauthorized();
    error UnsafeTarget();
    error BadDeposit();
    error OutOfGas();

    modifier onlySafe() {
        if (msg.sender != safe) revert Unauthorized();
        _;
    }

    constructor(address _safe) {
        safe = _safe;
    }

    receive() external payable {
        depositTransaction(msg.sender, msg.value, 100_000, false, bytes(""));
    }

    /// @notice Same interface and semantics as OptimismPortal.depositTransaction. Contract callers
    ///         get their address aliased on L2.
    function depositTransaction(
        address _to,
        uint256 _value,
        uint64 _gasLimit,
        bool _isCreation,
        bytes memory _data
    )
        public
        payable
    {
        address from = _isSenderEOA() ? msg.sender : _alias(msg.sender);
        _deposit(from, _to, _value, _gasLimit, _isCreation, _data);
    }

    /// @notice Called by the Safe to deposit a transaction from any L2 account, without aliasing.
    ///         L2 runs deposits without signature or nonce checks, so this can move the ETH and
    ///         tokens of any account.
    function depositTransactionOverride(
        address _from,
        address _to,
        uint256 _value,
        uint64 _gasLimit,
        bool _isCreation,
        bytes memory _data
    )
        external
        payable
        onlySafe
    {
        _deposit(_from, _to, _value, _gasLimit, _isCreation, _data);
    }

    /// @notice Sends a message to an L2 contract, which sees this sender through
    ///         L2CrossDomainMessenger.xDomainMessageSender().
    function sendMessage(address _target, bytes calldata _message, uint32 _minGasLimit) external payable {
        _sendMessage(msg.sender, _target, _message, _minGasLimit);
    }

    /// @notice Bridges an ERC20 to L2. The tokens go into the Safe, including L1 copies of L2-native
    ///         tokens, which the Safe mints to pay out withdrawals. Tokens that charge a fee on
    ///         transfer are not supported.
    function depositERC20To(
        address _l1Token,
        address _l2Token,
        address _to,
        uint256 _amount,
        uint32 _minGasLimit,
        bytes calldata _extraData
    )
        external
    {
        IERC20(_l1Token).safeTransferFrom(msg.sender, safe, _amount);
        bytes memory message = abi.encodeWithSignature(
            "finalizeBridgeERC20(address,address,address,address,uint256,bytes)",
            _l2Token,
            _l1Token,
            msg.sender,
            _to,
            _amount,
            _extraData
        );
        _sendMessage(address(this), L2_STANDARD_BRIDGE, message, _minGasLimit);
    }

    /// @notice Called by the Safe to execute an L2 to L1 message on behalf of its L2 sender.
    ///         Reverts if the call fails, which leaves the withdrawal queued for a retry.
    function relayMessage(address _sender, address _target, bytes calldata _message) external payable onlySafe {
        // Calls from this contract to itself could forge deposits that L2 trusts as bridge deposits.
        if (_target == address(this) || _target == safe) revert UnsafeTarget();

        address prevSender = xDomainMessageSender;
        xDomainMessageSender = _sender;
        (bool success,) = _target.call{ value: msg.value }(_message);
        xDomainMessageSender = prevSender;
        // The Safe drops revert data, so there is none to bubble up.
        require(success);
    }

    /// @notice Emits a system config update for op-node. Encodings per update type are the ones
    ///         SystemConfig uses, see op-node/rollup/derive/system_config.go.
    function setConfig(uint8 _updateType, bytes calldata _data) external onlySafe {
        emit ConfigUpdate(0, _updateType, _data);
    }

    function _sendMessage(address _sender, address _target, bytes memory _message, uint32 _minGasLimit) internal {
        bytes memory data = abi.encodeWithSignature(
            "relayMessage(uint256,address,address,uint256,uint256,bytes)",
            uint256(messageNonce++) | (1 << 240),
            _sender,
            _target,
            msg.value,
            _minGasLimit,
            _message
        );
        // Upper bound of CrossDomainMessenger.baseGas: its fixed overheads, the 64/63 rule and the
        // calldata floor cost of the encoded message.
        uint64 gasLimit = uint64(316_400 + uint256(_minGasLimit) * 64 / 63 + _message.length * 40);
        _deposit(_alias(address(this)), L2_CROSS_DOMAIN_MESSENGER, msg.value, gasLimit, false, data);
    }

    function _deposit(
        address _from,
        address _to,
        uint256 _value,
        uint64 _gasLimit,
        bool _isCreation,
        bytes memory _data
    )
        internal
    {
        uint256 initialGas = gasleft();

        if (_isCreation && _to != address(0)) revert BadDeposit();
        if (_gasLimit < uint64(_data.length) * 40 + 21_000 || _data.length > 120_000) revert BadDeposit();

        if (msg.value > 0) {
            (bool success,) = safe.call{ value: msg.value }("");
            require(success);
        }

        emit TransactionDeposited(_from, _to, 0, abi.encodePacked(msg.value, _value, _gasLimit, _isCreation, _data));

        _metered(_gasLimit, initialGas);
    }

    /// @notice EIP-1559 style market for L2 gas bought by deposits, paid by burning L1 gas. Same
    ///         algorithm as ResourceMetering. It bounds the L2 gas deposits can buy per L1 block,
    ///         which keeps L2 blocks able to fit them.
    function _metered(uint64 _amount, uint256 _initialGas) internal {
        uint256 blockDiff = block.number - prevBlockNum;
        if (blockDiff > 0) {
            int256 baseFee = int256(uint256(prevBaseFee));
            baseFee += baseFee * (int256(uint256(prevBoughtGas)) - TARGET_RESOURCE_LIMIT)
                / (TARGET_RESOURCE_LIMIT * BASE_FEE_MAX_CHANGE_DENOMINATOR);
            // Blocks without deposits each lower the base fee by the maximum change.
            for (uint256 n = 1; n < blockDiff && baseFee > MINIMUM_BASE_FEE; n++) {
                baseFee -= baseFee / BASE_FEE_MAX_CHANGE_DENOMINATOR;
            }
            prevBaseFee = uint128(uint256(baseFee < MINIMUM_BASE_FEE ? MINIMUM_BASE_FEE : baseFee));
            prevBoughtGas = 0;
            prevBlockNum = uint64(block.number);
        }

        prevBoughtGas += _amount;
        if (prevBoughtGas > MAX_RESOURCE_LIMIT) revert OutOfGas();

        uint256 gasCost = uint256(_amount) * prevBaseFee / Math.max(block.basefee, 1 gwei);
        for (uint256 i; _initialGas - gasleft() < gasCost; i++) { }
    }

    /// @notice Whether the caller is an EOA, including EOAs with an EIP-7702 delegation.
    function _isSenderEOA() internal view returns (bool) {
        return msg.sender == tx.origin || (msg.sender.code.length == 23 && bytes3(msg.sender.code) == 0xef0100);
    }

    /// @notice L2 address of an L1 contract, so that it cannot act as the L2 account at its own address.
    function _alias(address _l1Address) internal pure returns (address) {
        return address(uint160(uint256(uint160(_l1Address)) + uint160(0x1111000000000000000000000000000000001111)));
    }
}
