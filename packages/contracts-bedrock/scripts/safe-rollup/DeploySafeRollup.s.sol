// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { Script } from "forge-std/Script.sol";
import { Safe } from "safe-contracts/Safe.sol";
import { SafeProxyFactory } from "safe-contracts/proxies/SafeProxyFactory.sol";
import { Enum } from "safe-contracts/common/Enum.sol";
import { SafeRollupGateway } from "src/safe-rollup/SafeRollupGateway.sol";
import { WithdrawalDelayModule } from "src/safe-rollup/WithdrawalDelayModule.sol";

/// @notice Deploys the whole L1 side of a Safe rollup: a Safe, the gateway and the withdrawal module.
///         Env: OWNER_KEYS (comma separated, the cold keys), THRESHOLD, RELAYER, WITHDRAWAL_DELAY, OUT.
contract DeploySafeRollup is Script {
    function run() external {
        uint256[] memory ownerKeys = vm.envUint("OWNER_KEYS", ",");
        uint256 threshold = vm.envUint("THRESHOLD");
        address[] memory owners = new address[](ownerKeys.length);
        for (uint256 i = 0; i < ownerKeys.length; i++) {
            owners[i] = vm.addr(ownerKeys[i]);
        }

        vm.startBroadcast();
        Safe singleton = new Safe();
        SafeProxyFactory factory = new SafeProxyFactory();
        bytes memory setup = abi.encodeCall(
            Safe.setup, (owners, threshold, address(0), "", address(0), address(0), 0, payable(address(0)))
        );
        Safe safe = Safe(payable(address(factory.createProxyWithNonce(address(singleton), setup, 0))));
        SafeRollupGateway gateway = new SafeRollupGateway(address(safe));
        WithdrawalDelayModule module =
            new WithdrawalDelayModule(safe, vm.envAddress("RELAYER"), uint64(vm.envUint("WITHDRAWAL_DELAY")));
        execAsOwners(safe, ownerKeys, threshold, address(safe), abi.encodeCall(safe.enableModule, (address(module))));
        vm.stopBroadcast();

        string memory json = "addresses";
        vm.serializeAddress(json, "safe", address(safe));
        vm.serializeAddress(json, "gateway", address(gateway));
        string memory out = vm.serializeAddress(json, "module", address(module));
        vm.writeJson(out, vm.envString("OUT"));
    }

    /// @notice Signs and executes a Safe transaction with the first `_threshold` owners by address.
    function execAsOwners(
        Safe _safe,
        uint256[] memory _keys,
        uint256 _threshold,
        address _to,
        bytes memory _data
    )
        internal
    {
        bytes memory signatures = sign(_safe, _keys, _threshold, _to, _data);
        _safe.execTransaction(
            _to, 0, _data, Enum.Operation.Call, 0, 0, 0, address(0), payable(address(0)), signatures
        );
    }

    function sign(
        Safe _safe,
        uint256[] memory _keys,
        uint256 _threshold,
        address _to,
        bytes memory _data
    )
        internal
        view
        returns (bytes memory signatures_)
    {
        uint256 nonce = _safe.nonce();
        bytes32 txHash =
            _safe.getTransactionHash(_to, 0, _data, Enum.Operation.Call, 0, 0, 0, address(0), address(0), nonce);
        uint256[] memory keys = sortByAddress(_keys);
        for (uint256 i = 0; i < _threshold; i++) {
            (uint8 v, bytes32 r, bytes32 s) = vm.sign(keys[i], txHash);
            signatures_ = bytes.concat(signatures_, abi.encodePacked(r, s, v));
        }
    }

    function sortByAddress(uint256[] memory _keys) internal pure returns (uint256[] memory) {
        for (uint256 i = 1; i < _keys.length; i++) {
            for (uint256 j = i; j > 0 && vm.addr(_keys[j]) < vm.addr(_keys[j - 1]); j--) {
                (_keys[j], _keys[j - 1]) = (_keys[j - 1], _keys[j]);
            }
        }
        return _keys;
    }
}
