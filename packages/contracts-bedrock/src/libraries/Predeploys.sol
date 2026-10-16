// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

/// @title Predeploys
/// @notice Addresses of the protocol contracts predeployed on L2.
library Predeploys {
    address internal constant WETH = 0x4200000000000000000000000000000000000006;
    address internal constant L2_CROSS_DOMAIN_MESSENGER = 0x4200000000000000000000000000000000000007;
    address internal constant GAS_PRICE_ORACLE = 0x420000000000000000000000000000000000000F;
    address internal constant L2_STANDARD_BRIDGE = 0x4200000000000000000000000000000000000010;
    address internal constant SEQUENCER_FEE_WALLET = 0x4200000000000000000000000000000000000011;
    address internal constant OPTIMISM_MINTABLE_ERC20_FACTORY = 0x4200000000000000000000000000000000000012;
    address internal constant L1_BLOCK_ATTRIBUTES = 0x4200000000000000000000000000000000000015;
    address internal constant L2_TO_L1_MESSAGE_PASSER = 0x4200000000000000000000000000000000000016;
    address internal constant PROXY_ADMIN = 0x4200000000000000000000000000000000000018;
    address internal constant BASE_FEE_VAULT = 0x4200000000000000000000000000000000000019;
    address internal constant L1_FEE_VAULT = 0x420000000000000000000000000000000000001A;
    address internal constant OPERATOR_FEE_VAULT = 0x420000000000000000000000000000000000001b;

    /// @notice The L2 token address the bridge uses for ETH in its legacy withdraw functions.
    address internal constant LEGACY_ERC20_ETH = 0xDeadDeAddeAddEAddeadDEaDDEAdDeaDDeAD0000;

    /// @notice Address of a predeploy's implementation in the genesis state.
    function predeployToCodeNamespace(address _addr) internal pure returns (address) {
        return address(
            uint160((uint256(uint160(_addr)) & 0xffff) | uint256(uint160(0xc0D3C0d3C0d3C0D3c0d3C0d3c0D3C0d3c0d30000)))
        );
    }
}
