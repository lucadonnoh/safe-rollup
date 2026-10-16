// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

// Libraries

/// @title Encoding
/// @notice Encoding handles Optimism's various different encoding schemes.
library Encoding {
    /// @notice Thrown when a provided Super Root proof has an invalid version.
    error Encoding_InvalidSuperRootVersion();

    /// @notice Thrown when a provided Super Root proof has no Output Roots.
    error Encoding_EmptySuperRoot();

    /// @notice Thrown when attempting to decode an invalid Super Root Proof encoding.
    error Encoding_InvalidSuperRootEncoding();

    /// @notice Encodes a cross domain message based on the V0 (legacy) encoding.
    /// @param _target Address of the target of the message.
    /// @param _sender Address of the sender of the message.
    /// @param _data   Data to send with the message.
    /// @param _nonce  Message nonce.
    /// @return Encoded cross domain message.
    function encodeCrossDomainMessageV0(
        address _target,
        address _sender,
        bytes memory _data,
        uint256 _nonce
    )
        internal
        pure
        returns (bytes memory)
    {
        // nosemgrep: sol-style-use-abi-encodecall
        return abi.encodeWithSignature("relayMessage(address,address,bytes,uint256)", _target, _sender, _data, _nonce);
    }

    /// @notice Encodes a cross domain message based on the V1 (current) encoding.
    /// @param _nonce    Message nonce.
    /// @param _sender   Address of the sender of the message.
    /// @param _target   Address of the target of the message.
    /// @param _value    ETH value to send to the target.
    /// @param _gasLimit Gas limit to use for the message.
    /// @param _data     Data to send with the message.
    /// @return Encoded cross domain message.
    function encodeCrossDomainMessageV1(
        uint256 _nonce,
        address _sender,
        address _target,
        uint256 _value,
        uint256 _gasLimit,
        bytes memory _data
    )
        internal
        pure
        returns (bytes memory)
    {
        // nosemgrep: sol-style-use-abi-encodecall
        return abi.encodeWithSignature(
            "relayMessage(uint256,address,address,uint256,uint256,bytes)",
            _nonce,
            _sender,
            _target,
            _value,
            _gasLimit,
            _data
        );
    }

    /// @notice Adds a version number into the first two bytes of a message nonce.
    /// @param _nonce   Message nonce to encode into.
    /// @param _version Version number to encode into the message nonce.
    /// @return Message nonce with version encoded into the first two bytes.
    function encodeVersionedNonce(uint240 _nonce, uint16 _version) internal pure returns (uint256) {
        uint256 nonce;
        assembly {
            nonce := or(shl(240, _version), _nonce)
        }
        return nonce;
    }

    /// @notice Pulls the version out of a version-encoded nonce.
    /// @param _nonce Message nonce with version encoded into the first two bytes.
    /// @return Nonce without encoded version.
    /// @return Version of the message.
    function decodeVersionedNonce(uint256 _nonce) internal pure returns (uint240, uint16) {
        uint240 nonce;
        uint16 version;
        assembly {
            nonce := and(_nonce, 0x0000ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff)
            version := shr(240, _nonce)
        }
        return (nonce, version);
    }

}
