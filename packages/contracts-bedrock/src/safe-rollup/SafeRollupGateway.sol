// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { SafeERC20 } from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import { Math } from "@openzeppelin/contracts/utils/math/Math.sol";
import { ERC165Checker } from "@openzeppelin/contracts/utils/introspection/ERC165Checker.sol";
import { IOptimismMintableERC20 } from "interfaces/universal/IOptimismMintableERC20.sol";
import { AddressAliasHelper } from "src/vendor/AddressAliasHelper.sol";
import { Arithmetic } from "src/libraries/Arithmetic.sol";
import { Burn } from "src/libraries/Burn.sol";
import { EOA } from "src/libraries/EOA.sol";
import { Encoding } from "src/libraries/Encoding.sol";

/// @title SafeRollupGateway
/// @notice The only L1 contract of a Safe rollup besides the Safe and its withdrawal module. It
///         holds no funds: deposits are forwarded to the Safe in the same call.
///         - op-node reads it as the deposit contract (TransactionDeposited) and as the system
///           config contract (ConfigUpdate).
///         - The unchanged L2 predeploys see it as both the L1 messenger and the L1 standard bridge.
///         - The Safe calls it to relay L2 to L1 messages, so L1 targets can read the L2 sender.
contract SafeRollupGateway {
    using SafeERC20 for IERC20;

    struct ResourceParams {
        uint128 prevBaseFee;
        uint64 prevBoughtGas;
        uint64 prevBlockNum;
    }

    /// @notice Deposit gas market, with the default OP stack ResourceConfig values. It bounds the L2
    ///         gas that deposits can buy per L1 block, which keeps L2 blocks able to fit them.
    uint256 internal constant MAX_RESOURCE_LIMIT = 20_000_000;
    uint256 internal constant ELASTICITY_MULTIPLIER = 10;
    uint256 internal constant BASE_FEE_MAX_CHANGE_DENOMINATOR = 8;
    uint256 internal constant MINIMUM_BASE_FEE = 1 gwei;
    uint256 internal constant MAXIMUM_BASE_FEE = type(uint128).max;

    /// @notice L2 gas given to plain ETH transfers into this contract.
    uint64 internal constant RECEIVE_DEFAULT_GAS_LIMIT = 100_000;

    /// @notice Relay gas overheads of the L2CrossDomainMessenger, copied from CrossDomainMessenger.
    uint64 internal constant RELAY_CONSTANT_OVERHEAD = 200_000;
    uint64 internal constant MIN_GAS_DYNAMIC_OVERHEAD_NUMERATOR = 64;
    uint64 internal constant MIN_GAS_DYNAMIC_OVERHEAD_DENOMINATOR = 63;
    uint64 internal constant MIN_GAS_CALLDATA_OVERHEAD = 16;
    uint64 internal constant RELAY_CALL_OVERHEAD = 40_000;
    uint64 internal constant RELAY_RESERVED_GAS = 40_000;
    uint64 internal constant RELAY_GAS_CHECK_BUFFER = 5_000;
    uint64 internal constant TX_BASE_GAS = 21_000;
    uint64 internal constant FLOOR_CALLDATA_OVERHEAD = 40;
    uint64 internal constant ENCODING_OVERHEAD = 260;

    address internal constant L2_CROSS_DOMAIN_MESSENGER = 0x4200000000000000000000000000000000000007;
    address internal constant L2_STANDARD_BRIDGE = 0x4200000000000000000000000000000000000010;

    /// @notice The Safe that holds all bridged funds and is the only admin.
    address public immutable safe;

    ResourceParams public params;

    /// @notice Nonce of L1 to L2 messages, so that identical messages get distinct hashes on L2.
    uint240 public messageNonce;

    /// @notice L2 sender of the L2 to L1 message being relayed, zero outside of a relay.
    address public xDomainMessageSender;

    event TransactionDeposited(address indexed from, address indexed to, uint256 indexed version, bytes opaqueData);
    event ConfigUpdate(uint256 indexed version, uint8 indexed updateType, bytes data);

    error Unauthorized();
    error OutOfGas();
    error BadTarget();
    error GasLimitTooLow();
    error CalldataTooLarge();
    error UnsafeTarget();
    error WrongTokenPair();

    constructor(address _safe) {
        safe = _safe;
        params = ResourceParams({ prevBaseFee: 1 gwei, prevBoughtGas: 0, prevBlockNum: uint64(block.number) });
    }

    receive() external payable {
        depositTransaction(msg.sender, msg.value, RECEIVE_DEFAULT_GAS_LIMIT, false, bytes(""));
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
        address from = EOA.isSenderEOA() ? msg.sender : AddressAliasHelper.applyL1ToL2Alias(msg.sender);
        _deposit(from, _to, _value, _gasLimit, _isCreation, _data);
    }

    /// @notice Sends a message to an L2 contract, which sees this sender through
    ///         L2CrossDomainMessenger.xDomainMessageSender().
    function sendMessage(address _target, bytes calldata _message, uint32 _minGasLimit) external payable {
        _sendMessage(msg.sender, _target, _message, _minGasLimit);
    }

    /// @notice Bridges an ERC20 to L2. L1-native tokens go straight into the Safe and are minted on
    ///         L2. L1 copies of L2-native tokens (OptimismMintableERC20 with this gateway as
    ///         bridge) are burned and released from the L2 bridge. Tokens that charge a fee on
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
        if (ERC165Checker.supportsInterface(_l1Token, type(IOptimismMintableERC20).interfaceId)) {
            if (IOptimismMintableERC20(_l1Token).remoteToken() != _l2Token) revert WrongTokenPair();
            IOptimismMintableERC20(_l1Token).burn(msg.sender, _amount);
        } else {
            IERC20(_l1Token).safeTransferFrom(msg.sender, safe, _amount);
        }
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

    /// @notice Called by the Safe to pay out a withdrawal of an L2-native token by minting its L1 copy.
    function mintERC20(address _l1Token, address _to, uint256 _amount) external {
        if (msg.sender != safe) revert Unauthorized();
        IOptimismMintableERC20(_l1Token).mint(_to, _amount);
    }

    /// @notice Called by the Safe to execute an L2 to L1 message on behalf of its L2 sender.
    ///         Reverts if the call fails, which leaves the withdrawal queued for a retry.
    function relayMessage(address _sender, address _target, bytes calldata _message) external payable {
        if (msg.sender != safe) revert Unauthorized();
        // Calls from this contract to itself could forge deposits that L2 trusts as bridge deposits.
        if (_target == address(this) || _target == safe) revert UnsafeTarget();

        address prevSender = xDomainMessageSender;
        xDomainMessageSender = _sender;
        (bool success, bytes memory returnData) = _target.call{ value: msg.value }(_message);
        xDomainMessageSender = prevSender;

        if (!success) {
            assembly {
                revert(add(returnData, 32), mload(returnData))
            }
        }
    }

    /// @notice Emits a system config update for op-node. Encodings per update type are the ones
    ///         SystemConfig uses, see op-node/rollup/derive/system_config.go.
    function setConfig(uint8 _updateType, bytes calldata _data) external {
        if (msg.sender != safe) revert Unauthorized();
        emit ConfigUpdate(0, _updateType, _data);
    }

    /// @notice L2 gas needed to relay a message with the given minimum gas limit, same formula as
    ///         CrossDomainMessenger.baseGas.
    function baseGas(bytes memory _message, uint32 _minGasLimit) public pure returns (uint64) {
        uint64 executionGas = uint64(
            RELAY_CONSTANT_OVERHEAD + RELAY_CALL_OVERHEAD + RELAY_RESERVED_GAS + RELAY_GAS_CHECK_BUFFER
                + ((_minGasLimit * MIN_GAS_DYNAMIC_OVERHEAD_NUMERATOR) / MIN_GAS_DYNAMIC_OVERHEAD_DENOMINATOR)
        );
        uint64 totalMessageSize = uint64(_message.length + ENCODING_OVERHEAD);
        return TX_BASE_GAS
            + uint64(
                Math.max(
                    executionGas + (totalMessageSize * MIN_GAS_CALLDATA_OVERHEAD),
                    totalMessageSize * FLOOR_CALLDATA_OVERHEAD
                )
            );
    }

    function _sendMessage(address _sender, address _target, bytes memory _message, uint32 _minGasLimit) internal {
        bytes memory data = Encoding.encodeCrossDomainMessageV1(
            Encoding.encodeVersionedNonce(messageNonce, 1), _sender, _target, msg.value, _minGasLimit, _message
        );
        messageNonce++;
        _deposit(
            AddressAliasHelper.applyL1ToL2Alias(address(this)),
            L2_CROSS_DOMAIN_MESSENGER,
            msg.value,
            baseGas(_message, _minGasLimit),
            false,
            data
        );
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

        if (_isCreation && _to != address(0)) revert BadTarget();
        if (_gasLimit < uint64(_data.length) * 40 + 21_000) revert GasLimitTooLow();
        if (_data.length > 120_000) revert CalldataTooLarge();

        if (msg.value > 0) {
            (bool success,) = safe.call{ value: msg.value }("");
            require(success);
        }

        emit TransactionDeposited(_from, _to, 0, abi.encodePacked(msg.value, _value, _gasLimit, _isCreation, _data));

        _metered(_gasLimit, initialGas);
    }

    /// @notice EIP-1559 style market for L2 gas bought by deposits, paid by burning L1 gas. Same
    ///         algorithm as ResourceMetering.
    function _metered(uint64 _amount, uint256 _initialGas) internal {
        uint256 blockDiff = block.number - params.prevBlockNum;
        int256 targetResourceLimit = int256(MAX_RESOURCE_LIMIT / ELASTICITY_MULTIPLIER);

        if (blockDiff > 0) {
            int256 gasUsedDelta = int256(uint256(params.prevBoughtGas)) - targetResourceLimit;
            int256 baseFeeDelta = (int256(uint256(params.prevBaseFee)) * gasUsedDelta)
                / (targetResourceLimit * int256(BASE_FEE_MAX_CHANGE_DENOMINATOR));
            int256 newBaseFee = Arithmetic.clamp({
                _value: int256(uint256(params.prevBaseFee)) + baseFeeDelta,
                _min: int256(MINIMUM_BASE_FEE),
                _max: int256(MAXIMUM_BASE_FEE)
            });
            if (blockDiff > 1) {
                newBaseFee = Arithmetic.clamp({
                    _value: Arithmetic.cdexp({
                        _coefficient: newBaseFee,
                        _denominator: int256(BASE_FEE_MAX_CHANGE_DENOMINATOR),
                        _exponent: int256(blockDiff - 1)
                    }),
                    _min: int256(MINIMUM_BASE_FEE),
                    _max: int256(MAXIMUM_BASE_FEE)
                });
            }
            params.prevBaseFee = uint128(uint256(newBaseFee));
            params.prevBoughtGas = 0;
            params.prevBlockNum = uint64(block.number);
        }

        params.prevBoughtGas += _amount;
        if (params.prevBoughtGas > MAX_RESOURCE_LIMIT) revert OutOfGas();

        uint256 gasCost = uint256(_amount) * uint256(params.prevBaseFee) / Math.max(block.basefee, 1 gwei);
        uint256 usedGas = _initialGas - gasleft();
        if (gasCost > usedGas) Burn.gas(gasCost - usedGas);
    }
}
