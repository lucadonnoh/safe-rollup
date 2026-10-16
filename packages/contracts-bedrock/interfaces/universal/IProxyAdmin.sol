// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

interface IProxyAdmin {
    event OwnershipTransferred(address indexed previousOwner, address indexed newOwner);

    function changeProxyAdmin(address payable _proxy, address _newAdmin) external;
    function getProxyAdmin(address payable _proxy) external view returns (address);
    function getProxyImplementation(address _proxy) external view returns (address);
    function owner() external view returns (address);
    function renounceOwnership() external;
    function transferOwnership(address newOwner) external; // nosemgrep
    function upgrade(address payable _proxy, address _implementation) external;
    function upgradeAndCall(address payable _proxy, address _implementation, bytes memory _data) external payable;

    function __constructor__(address _owner) external;
}
