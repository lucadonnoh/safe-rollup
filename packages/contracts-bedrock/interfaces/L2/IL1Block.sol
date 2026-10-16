// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

interface IL1Block {
    function DEPOSITOR_ACCOUNT() external pure returns (address addr_);
    function baseFeeScalar() external view returns (uint32);
    function basefee() external view returns (uint256);
    function batcherHash() external view returns (bytes32);
    function blobBaseFee() external view returns (uint256);
    function blobBaseFeeScalar() external view returns (uint32);
    function gasPayingTokenName() external pure returns (string memory name_);
    function gasPayingTokenSymbol() external pure returns (string memory symbol_);
    function hash() external view returns (bytes32);
    function l1FeeOverhead() external view returns (uint256);
    function l1FeeScalar() external view returns (uint256);
    function number() external view returns (uint64);
    function operatorFeeScalar() external view returns (uint32);
    function operatorFeeConstant() external view returns (uint64);
    function daFootprintGasScalar() external view returns (uint16);
    function sequenceNumber() external view returns (uint64);
    function setL1BlockValuesJovian() external;
    function timestamp() external view returns (uint64);
    function version() external pure returns (string memory);

    function __constructor__() external;
}
