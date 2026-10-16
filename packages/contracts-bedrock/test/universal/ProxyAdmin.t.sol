// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { Test } from "test/setup/Test.sol";
import { Proxy_SimpleStorage_Harness } from "test/universal/Proxy.t.sol";
import { IProxy } from "interfaces/universal/IProxy.sol";
import { IProxyAdmin } from "interfaces/universal/IProxyAdmin.sol";

contract ProxyAdmin_Test is Test {
    address alice = makeAddr("alice");
    IProxyAdmin admin;
    IProxy proxy;
    Proxy_SimpleStorage_Harness implementation;

    function setUp() public {
        admin = IProxyAdmin(vm.deployCode("ProxyAdmin.sol:ProxyAdmin", abi.encode(alice)));
        proxy = IProxy(payable(vm.deployCode("Proxy.sol:Proxy", abi.encode(address(admin)))));
        implementation = new Proxy_SimpleStorage_Harness();
    }

    function test_owner_succeeds() external view {
        assertEq(admin.owner(), alice);
    }

    function test_upgrade_succeeds() external {
        vm.prank(alice);
        admin.upgrade(payable(address(proxy)), address(implementation));
        assertEq(admin.getProxyImplementation(address(proxy)), address(implementation));
    }

    function test_upgradeAndCall_succeeds() external {
        vm.prank(alice);
        admin.upgradeAndCall(
            payable(address(proxy)),
            address(implementation),
            abi.encodeCall(Proxy_SimpleStorage_Harness.set, (1, 2))
        );
        assertEq(admin.getProxyImplementation(address(proxy)), address(implementation));
        assertEq(Proxy_SimpleStorage_Harness(address(proxy)).get(1), 2);
    }

    function test_changeProxyAdmin_succeeds() external {
        assertEq(admin.getProxyAdmin(payable(address(proxy))), address(admin));
        vm.prank(alice);
        admin.changeProxyAdmin(payable(address(proxy)), address(128));
        vm.prank(address(128));
        assertEq(proxy.admin(), address(128));
    }

    function test_onlyOwner_notOwner_reverts() external {
        vm.expectRevert("Ownable: caller is not the owner");
        admin.upgrade(payable(address(proxy)), address(implementation));
        vm.expectRevert("Ownable: caller is not the owner");
        admin.upgradeAndCall(payable(address(proxy)), address(implementation), "");
        vm.expectRevert("Ownable: caller is not the owner");
        admin.changeProxyAdmin(payable(address(proxy)), alice);
    }
}
