// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

// Libraries
import { LibZip } from "@solady/utils/LibZip.sol";
import { Predeploys } from "src/libraries/Predeploys.sol";

// Interfaces
import { ISemver } from "interfaces/universal/ISemver.sol";
import { IL1Block } from "interfaces/L2/IL1Block.sol";

/// @custom:proxied true
/// @custom:predeploy 0x420000000000000000000000000000000000000F
/// @title GasPriceOracle
/// @notice Estimates the L1 portion of a transaction fee, using the Jovian fee formulas. Every fork
///         up to Jovian is active from genesis.
contract GasPriceOracle is ISemver {
    /// @notice Number of decimals used in the scalar.
    uint256 public constant DECIMALS = 6;

    /// @custom:semver 1.6.0
    string public constant version = "1.6.0";

    /// @notice Intercept and FastLZ coefficient of the linear regression that estimates the
    ///         Brotli-compressed size of a transaction.
    int32 private constant COST_INTERCEPT = -42_585_600;
    uint32 private constant COST_FASTLZ_COEF = 836_500;

    /// @notice Minimum estimated size of a transaction, in bytes.
    uint256 private constant MIN_TRANSACTION_SIZE = 100;

    /// @notice Fee formula flags, kept for tools that read them.
    bool public constant isEcotone = true;
    bool public constant isFjord = true;
    bool public constant isIsthmus = true;
    bool public constant isJovian = true;

    /// @notice Computes the L1 portion of the fee for an RLP-encoded, unsigned transaction.
    function getL1Fee(bytes memory _data) external view returns (uint256) {
        return _l1Cost(LibZip.flzCompress(_data).length + 68);
    }

    /// @notice Upper bound of the L1 fee for a transaction of the given unsigned size.
    function getL1FeeUpperBound(uint256 _unsignedTxSize) external view returns (uint256) {
        uint256 txSize = _unsignedTxSize + 68;
        // txSize / 255 + 16 is the practical FastLZ upper bound, covering 99.99% of transactions.
        return _l1Cost(txSize + txSize / 255 + 16);
    }

    function gasPrice() public view returns (uint256) {
        return block.basefee;
    }

    function baseFee() public view returns (uint256) {
        return block.basefee;
    }

    function l1BaseFee() public view returns (uint256) {
        return IL1Block(Predeploys.L1_BLOCK_ATTRIBUTES).basefee();
    }

    function blobBaseFee() public view returns (uint256) {
        return IL1Block(Predeploys.L1_BLOCK_ATTRIBUTES).blobBaseFee();
    }

    function baseFeeScalar() public view returns (uint32) {
        return IL1Block(Predeploys.L1_BLOCK_ATTRIBUTES).baseFeeScalar();
    }

    function blobBaseFeeScalar() public view returns (uint32) {
        return IL1Block(Predeploys.L1_BLOCK_ATTRIBUTES).blobBaseFeeScalar();
    }

    function decimals() public pure returns (uint256) {
        return DECIMALS;
    }

    /// @notice Estimated L1 gas of a transaction, assuming its compressed data is mostly non-zero.
    function getL1GasUsed(bytes memory _data) public pure returns (uint256) {
        return _estimatedSize(LibZip.flzCompress(_data).length + 68) * 16 / 1e6;
    }

    function getOperatorFee(uint256 _gasUsed) public view returns (uint256) {
        uint256 operatorScalar = IL1Block(Predeploys.L1_BLOCK_ATTRIBUTES).operatorFeeScalar();
        uint256 operatorConstant = IL1Block(Predeploys.L1_BLOCK_ATTRIBUTES).operatorFeeConstant();
        return _gasUsed * operatorScalar * 100 + operatorConstant;
    }

    function _l1Cost(uint256 _fastLzSize) internal view returns (uint256) {
        uint256 feeScaled = baseFeeScalar() * 16 * l1BaseFee() + blobBaseFeeScalar() * blobBaseFee();
        return _estimatedSize(_fastLzSize) * feeScaled / (10 ** (DECIMALS * 2));
    }

    /// @notice Estimated Brotli-compressed size, scaled by 1e6.
    function _estimatedSize(uint256 _fastLzSize) internal pure returns (uint256) {
        int256 estimatedSize = COST_INTERCEPT + int256(COST_FASTLZ_COEF * _fastLzSize);
        if (estimatedSize < int256(MIN_TRANSACTION_SIZE) * 1e6) {
            estimatedSize = int256(MIN_TRANSACTION_SIZE) * 1e6;
        }
        return uint256(estimatedSize);
    }
}
