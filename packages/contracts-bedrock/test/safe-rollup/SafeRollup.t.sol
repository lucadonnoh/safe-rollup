// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { Test } from "forge-std/Test.sol";
import { ERC20 } from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import { Safe } from "safe-contracts/Safe.sol";
import { SafeProxyFactory } from "safe-contracts/proxies/SafeProxyFactory.sol";
import { Enum } from "safe-contracts/common/Enum.sol";
import { AddressAliasHelper } from "src/vendor/AddressAliasHelper.sol";
import { Encoding } from "src/libraries/Encoding.sol";
import { OptimismMintableERC20 } from "src/universal/OptimismMintableERC20.sol";
import { SafeRollupGateway } from "src/safe-rollup/SafeRollupGateway.sol";
import { WithdrawalDelayModule } from "src/safe-rollup/WithdrawalDelayModule.sol";

contract Token is ERC20 {
    constructor(address _holder) ERC20("Token", "TKN") {
        _mint(_holder, 1000 ether);
    }
}

/// @notice L1 contract that only accepts calls relayed for one L2 sender, like a bridge app would.
contract L1App {
    SafeRollupGateway internal immutable gateway;
    address internal immutable l2App;
    uint256 public received;

    constructor(SafeRollupGateway _gateway, address _l2App) {
        gateway = _gateway;
        l2App = _l2App;
    }

    function receiveFromL2(uint256 _amount) external payable {
        require(msg.sender == address(gateway) && gateway.xDomainMessageSender() == l2App, "not from L2 app");
        received += _amount;
    }
}

contract SafeRollup_Test is Test {
    event TransactionDeposited(address indexed from, address indexed to, uint256 indexed version, bytes opaqueData);
    event ConfigUpdate(uint256 indexed version, uint8 indexed updateType, bytes data);

    uint256 internal constant DELAY = 7 days;
    uint256 internal ownerKey = 0xA11CE;
    address internal relayer = makeAddr("relayer");
    address internal alice = makeAddr("alice");

    Safe internal safe;
    SafeRollupGateway internal gateway;
    WithdrawalDelayModule internal module;

    function setUp() public {
        address[] memory owners = new address[](1);
        owners[0] = vm.addr(ownerKey);
        bytes memory setup = abi.encodeCall(
            Safe.setup, (owners, 1, address(0), "", address(0), address(0), 0, payable(address(0)))
        );
        safe = Safe(payable(address(new SafeProxyFactory().createProxyWithNonce(address(new Safe()), setup, 0))));
        gateway = new SafeRollupGateway(address(safe));
        module = new WithdrawalDelayModule(safe, relayer, uint64(DELAY));
        execAsOwner(address(safe), abi.encodeCall(safe.enableModule, (address(module))));
        vm.deal(alice, 10 ether);
        vm.deal(address(safe), 10 ether);
    }

    function execAsOwner(address _to, bytes memory _data) internal {
        bytes32 txHash = safe.getTransactionHash(
            _to, 0, _data, Enum.Operation.Call, 0, 0, 0, address(0), address(0), safe.nonce()
        );
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(ownerKey, txHash);
        safe.execTransaction(
            _to, 0, _data, Enum.Operation.Call, 0, 0, 0, address(0), payable(address(0)), abi.encodePacked(r, s, v)
        );
    }

    // Gateway.

    function test_depositTransaction_eoa_forwardsToSafe() public {
        vm.expectEmit(address(gateway));
        emit TransactionDeposited(alice, alice, 0, abi.encodePacked(uint256(1 ether), uint256(1 ether), uint64(100_000), false, ""));
        vm.prank(alice, alice);
        gateway.depositTransaction{ value: 1 ether }(alice, 1 ether, 100_000, false, "");
        assertEq(address(safe).balance, 11 ether);
        assertEq(address(gateway).balance, 0);
    }

    function test_depositTransaction_contract_isAliased() public {
        address l1Contract = address(new L1App(gateway, address(0)));
        vm.expectEmit(address(gateway));
        emit TransactionDeposited(
            AddressAliasHelper.applyL1ToL2Alias(l1Contract), alice, 0, abi.encodePacked(uint256(0), uint256(0), uint64(100_000), false, "")
        );
        vm.prank(l1Contract, alice);
        gateway.depositTransaction(alice, 0, 100_000, false, "");
    }

    function test_depositTransaction_gasLimitTooLow_reverts() public {
        vm.expectRevert(SafeRollupGateway.GasLimitTooLow.selector);
        vm.prank(alice, alice);
        gateway.depositTransaction(alice, 0, 20_000, false, "");
    }

    function test_receive_depositsToSender() public {
        vm.prank(alice, alice);
        (bool success,) = address(gateway).call{ value: 1 ether }("");
        assertTrue(success);
        assertEq(address(safe).balance, 11 ether);
    }

    function test_sendMessage_encodesRelayAndIncrementsNonce() public {
        bytes memory message = hex"1234";
        bytes memory relay = Encoding.encodeCrossDomainMessageV1(
            Encoding.encodeVersionedNonce(0, 1), alice, address(0xbeef), 0, 50_000, message
        );
        vm.expectEmit(address(gateway));
        emit TransactionDeposited(
            AddressAliasHelper.applyL1ToL2Alias(address(gateway)),
            0x4200000000000000000000000000000000000007,
            0,
            abi.encodePacked(uint256(0), uint256(0), gateway.baseGas(message, 50_000), false, relay)
        );
        vm.prank(alice);
        gateway.sendMessage(address(0xbeef), message, 50_000);
        assertEq(gateway.messageNonce(), 1);
    }

    function test_depositERC20To_movesTokensToSafe() public {
        Token token = new Token(alice);
        vm.startPrank(alice);
        token.approve(address(gateway), 5 ether);
        gateway.depositERC20To(address(token), address(0x1234), alice, 5 ether, 200_000, "");
        vm.stopPrank();
        assertEq(token.balanceOf(address(safe)), 5 ether);
        assertEq(token.balanceOf(address(gateway)), 0);
    }

    function test_depositERC20To_burnsL1CopyOfL2NativeToken() public {
        address l2Token = makeAddr("l2Token");
        OptimismMintableERC20 l1Copy = new OptimismMintableERC20(address(gateway), l2Token, "L2 Token", "L2T", 18);
        vm.prank(address(safe));
        gateway.mintERC20(address(l1Copy), alice, 5 ether);

        vm.prank(alice);
        gateway.depositERC20To(address(l1Copy), l2Token, alice, 3 ether, 200_000, "");
        assertEq(l1Copy.balanceOf(alice), 2 ether);
        assertEq(l1Copy.totalSupply(), 2 ether);
        assertEq(l1Copy.balanceOf(address(safe)), 0);
    }

    function test_depositERC20To_wrongPair_reverts() public {
        OptimismMintableERC20 l1Copy =
            new OptimismMintableERC20(address(gateway), makeAddr("l2Token"), "L2 Token", "L2T", 18);
        vm.prank(address(safe));
        gateway.mintERC20(address(l1Copy), alice, 1 ether);

        vm.expectRevert(SafeRollupGateway.WrongTokenPair.selector);
        vm.prank(alice);
        gateway.depositERC20To(address(l1Copy), makeAddr("otherToken"), alice, 1 ether, 200_000, "");
    }

    function test_mintERC20_onlySafe_reverts() public {
        vm.expectRevert(SafeRollupGateway.Unauthorized.selector);
        gateway.mintERC20(address(1), alice, 1);
    }

    function test_relayMessage_onlySafe_reverts() public {
        vm.expectRevert(SafeRollupGateway.Unauthorized.selector);
        gateway.relayMessage(alice, alice, "");
    }

    function test_relayMessage_unsafeTargets_revert() public {
        vm.startPrank(address(safe));
        vm.expectRevert(SafeRollupGateway.UnsafeTarget.selector);
        gateway.relayMessage(alice, address(gateway), "");
        vm.expectRevert(SafeRollupGateway.UnsafeTarget.selector);
        gateway.relayMessage(alice, address(safe), "");
        vm.stopPrank();
    }

    function test_setConfig_emitsForSafeOnly() public {
        vm.expectRevert(SafeRollupGateway.Unauthorized.selector);
        gateway.setConfig(0, abi.encode(alice));
        vm.expectEmit(address(gateway));
        emit ConfigUpdate(0, 0, abi.encode(alice));
        vm.prank(address(safe));
        gateway.setConfig(0, abi.encode(alice));
    }

    // Module.

    function test_payout_afterDelay_succeeds() public {
        bytes32 hash = keccak256("withdrawal");
        vm.prank(relayer);
        module.queue(hash, alice, 1 ether, "");

        vm.expectRevert(WithdrawalDelayModule.TooEarly.selector);
        module.execute(hash, alice, 1 ether, "");

        vm.warp(block.timestamp + DELAY);
        vm.expectRevert(WithdrawalDelayModule.WrongCall.selector);
        module.execute(hash, alice, 2 ether, "");

        module.execute(hash, alice, 1 ether, "");
        assertEq(alice.balance, 11 ether);
        vm.expectRevert(WithdrawalDelayModule.AlreadyExecuted.selector);
        module.execute(hash, alice, 1 ether, "");
    }

    function test_payout_mintsL2NativeToken() public {
        OptimismMintableERC20 l1Copy =
            new OptimismMintableERC20(address(gateway), makeAddr("l2Token"), "L2 Token", "L2T", 18);
        bytes memory mint = abi.encodeCall(gateway.mintERC20, (address(l1Copy), alice, 4 ether));
        bytes32 hash = keccak256("l2 native withdrawal");
        vm.prank(relayer);
        module.queue(hash, address(gateway), 0, mint);
        vm.warp(block.timestamp + DELAY);
        module.execute(hash, address(gateway), 0, mint);
        assertEq(l1Copy.balanceOf(alice), 4 ether);
    }

    function test_relayedMessage_seesL2Sender() public {
        address l2App = makeAddr("l2App");
        L1App app = new L1App(gateway, l2App);
        bytes memory relay = abi.encodeCall(
            gateway.relayMessage, (l2App, address(app), abi.encodeCall(app.receiveFromL2, (7)))
        );
        bytes32 hash = keccak256("message");
        vm.prank(relayer);
        module.queue(hash, address(gateway), 1 ether, relay);
        vm.warp(block.timestamp + DELAY);
        module.execute(hash, address(gateway), 1 ether, relay);
        assertEq(app.received(), 7);
        assertEq(address(app).balance, 1 ether);
        assertEq(gateway.xDomainMessageSender(), address(0));
    }

    function test_failedPayout_canBeRetried() public {
        address l2App = makeAddr("l2App");
        L1App app = new L1App(gateway, l2App);
        // Wrong L2 sender, so the target reverts.
        bytes memory relay = abi.encodeCall(
            gateway.relayMessage, (alice, address(app), abi.encodeCall(app.receiveFromL2, (7)))
        );
        bytes32 hash = keccak256("message");
        vm.prank(relayer);
        module.queue(hash, address(gateway), 0, relay);
        vm.warp(block.timestamp + DELAY);
        vm.expectRevert(WithdrawalDelayModule.CallFailed.selector);
        module.execute(hash, address(gateway), 0, relay);
        (,, bool executed) = module.items(hash);
        assertFalse(executed);
    }

    function test_veto_blocksPayout() public {
        bytes32 hash = keccak256("fake");
        vm.prank(relayer);
        module.queue(hash, alice, 5 ether, "");
        execAsOwner(address(module), abi.encodeCall(module.veto, (hash)));
        vm.warp(block.timestamp + DELAY);
        vm.expectRevert(WithdrawalDelayModule.NotQueued.selector);
        module.execute(hash, alice, 5 ether, "");
        assertEq(address(safe).balance, 10 ether);
    }

    function test_queue_accessAndTargets() public {
        vm.expectRevert(WithdrawalDelayModule.Unauthorized.selector);
        module.queue(bytes32(0), alice, 0, "");

        vm.startPrank(relayer);
        vm.expectRevert(WithdrawalDelayModule.UnsafeTarget.selector);
        module.queue(bytes32(0), address(safe), 0, abi.encodeCall(safe.enableModule, (alice)));
        vm.expectRevert(WithdrawalDelayModule.UnsafeTarget.selector);
        module.queue(bytes32(0), address(module), 0, "");
        module.queue(bytes32(0), alice, 0, "");
        vm.expectRevert(WithdrawalDelayModule.AlreadyQueued.selector);
        module.queue(bytes32(0), alice, 0, "");
        vm.stopPrank();
    }

    function test_adminFunctions_onlySafe() public {
        vm.expectRevert(WithdrawalDelayModule.Unauthorized.selector);
        module.veto(bytes32(0));
        vm.expectRevert(WithdrawalDelayModule.Unauthorized.selector);
        module.setRelayer(alice);
        vm.expectRevert(WithdrawalDelayModule.Unauthorized.selector);
        module.setDelay(0);

        execAsOwner(address(module), abi.encodeCall(module.setRelayer, (alice)));
        assertEq(module.relayer(), alice);
    }
}
