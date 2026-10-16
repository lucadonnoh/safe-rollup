// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { Ownable } from "@openzeppelin/contracts/access/Ownable.sol";
import { IProxy } from "interfaces/universal/IProxy.sol";
import { IStaticERC1967Proxy } from "interfaces/universal/IStaticERC1967Proxy.sol";

/// @title ProxyAdmin
/// @notice Admin of the predeploy proxies. Its owner is the L2 alias of the Safe, so the Safe
///         upgrades L2 contracts by sending a deposit.
contract ProxyAdmin is Ownable {
    constructor(address _owner) Ownable() {
        _transferOwnership(_owner);
    }

    function getProxyImplementation(address _proxy) external view returns (address) {
        return IStaticERC1967Proxy(_proxy).implementation();
    }

    function getProxyAdmin(address payable _proxy) external view returns (address) {
        return IStaticERC1967Proxy(_proxy).admin();
    }

    function changeProxyAdmin(address payable _proxy, address _newAdmin) external onlyOwner {
        IProxy(_proxy).changeAdmin(_newAdmin);
    }

    function upgrade(address payable _proxy, address _implementation) external onlyOwner {
        IProxy(_proxy).upgradeTo(_implementation);
    }

    function upgradeAndCall(
        address payable _proxy,
        address _implementation,
        bytes memory _data
    )
        external
        payable
        onlyOwner
    {
        IProxy(_proxy).upgradeToAndCall{ value: msg.value }(_implementation, _data);
    }
}
