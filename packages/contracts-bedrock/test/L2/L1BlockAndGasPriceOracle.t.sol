// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { Test } from "test/setup/Test.sol";
import { Constants } from "src/libraries/Constants.sol";
import { Predeploys } from "src/libraries/Predeploys.sol";
import { IL1Block } from "interfaces/L2/IL1Block.sol";
import { IGasPriceOracle } from "interfaces/L2/IGasPriceOracle.sol";

/// @notice Tests the Jovian L1 info setter and the fee estimates built on it. Fee vectors are the
///         upstream Fjord ones, which Jovian keeps.
contract L1BlockAndGasPriceOracle_Test is Test {
    IL1Block l1Block = IL1Block(Predeploys.L1_BLOCK_ATTRIBUTES);
    IGasPriceOracle oracle = IGasPriceOracle(Predeploys.GAS_PRICE_ORACLE);

    uint32 constant baseFeeScalar = 20;
    uint32 constant blobBaseFeeScalar = 15;
    uint64 constant sequenceNumber = 0;
    uint64 constant timestamp = 11;
    uint64 constant number = 10;
    uint256 constant baseFee = 2 * (10 ** 6);
    uint256 constant blobBaseFee = 3 * (10 ** 6);
    bytes32 constant hash = bytes32(uint256(64));
    bytes32 constant batcherHash = bytes32(uint256(777));
    uint32 constant operatorFeeScalar = 4_000_000;
    uint64 constant operatorFeeConstant = 300;
    uint16 constant daFootprintGasScalar = 400;

    function setUp() public {
        vm.etch(address(l1Block), vm.getDeployedCode("L1Block.sol:L1Block"));
        vm.etch(address(oracle), vm.getDeployedCode("GasPriceOracle.sol:GasPriceOracle"));
        vm.prank(Constants.DEPOSITOR_ACCOUNT);
        (bool success,) = address(l1Block).call(jovianCalldata());
        require(success, "setL1BlockValuesJovian failed");
    }

    function jovianCalldata() internal pure returns (bytes memory) {
        return abi.encodePacked(
            abi.encodePacked(
                bytes4(keccak256("setL1BlockValuesJovian()")),
                baseFeeScalar,
                blobBaseFeeScalar,
                sequenceNumber,
                timestamp,
                number,
                baseFee
            ),
            abi.encodePacked(
                blobBaseFee, hash, batcherHash, operatorFeeScalar, operatorFeeConstant, daFootprintGasScalar
            )
        );
    }

    function test_setL1BlockValuesJovian_succeeds() external view {
        assertEq(l1Block.baseFeeScalar(), baseFeeScalar);
        assertEq(l1Block.blobBaseFeeScalar(), blobBaseFeeScalar);
        assertEq(l1Block.sequenceNumber(), sequenceNumber);
        assertEq(l1Block.timestamp(), timestamp);
        assertEq(l1Block.number(), number);
        assertEq(l1Block.basefee(), baseFee);
        assertEq(l1Block.blobBaseFee(), blobBaseFee);
        assertEq(l1Block.hash(), hash);
        assertEq(l1Block.batcherHash(), batcherHash);
        assertEq(l1Block.operatorFeeScalar(), operatorFeeScalar);
        assertEq(l1Block.operatorFeeConstant(), operatorFeeConstant);
        assertEq(l1Block.daFootprintGasScalar(), daFootprintGasScalar);
    }

    function test_setL1BlockValuesJovian_notDepositor_reverts() external {
        (bool success, bytes memory data) = address(l1Block).call(jovianCalldata());
        assertFalse(success);
        assertEq(data, abi.encodeWithSignature("NotDepositor()"));
    }

    function test_flags_succeeds() external view {
        assertTrue(oracle.isEcotone() && oracle.isFjord() && oracle.isIsthmus() && oracle.isJovian());
        assertEq(oracle.decimals(), 6);
    }

    function test_getL1Fee_minimumBound_succeeds() external view {
        bytes memory data = hex"0000010203";
        assertEq(oracle.getL1GasUsed(data), 1600);
        assertEq(oracle.getL1Fee(data), 68500);
        assertEq(oracle.getL1FeeUpperBound(data.length), 68500);
    }

    function test_getL1Fee_regression_succeeds() external view {
        bytes memory data = hex"1d2c3ec4f5a9b3f3cd2c024e455c1143a74bbd637c324adcbd4f74e346786ac44e23e78f47d932abedd8d1"
            hex"06daadcea350be16478461046273101034601364012364701331dfad43729dc486abd134bcad61b34d6ca1"
            hex"f2eb31655b7d61ca33ba6d172cdf7d8b5b0ef389a314ca7a9a831c09fc2ca9090d059b4dd25194f3de297b"
            hex"dba6d6d796e4f80be94f8a9151d685607826e7ba25177b40cb127ea9f1438470";
        assertEq(oracle.getL1GasUsed(data), 2463);
        assertEq(oracle.getL1Fee(data), 105484);
        assertEq(oracle.getL1FeeUpperBound(data.length), 111214);
    }

    function test_getOperatorFee_succeeds() external view {
        assertEq(oracle.getOperatorFee(10), 10 * operatorFeeScalar * 100 + operatorFeeConstant);
    }
}
