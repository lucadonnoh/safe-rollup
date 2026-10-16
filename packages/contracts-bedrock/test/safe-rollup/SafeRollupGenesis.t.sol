// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { Test } from "test/setup/Test.sol";
import { SafeRollupGenesis } from "scripts/safe-rollup/SafeRollupGenesis.s.sol";
import { Predeploys } from "src/libraries/Predeploys.sol";
import { Preinstalls } from "src/libraries/Preinstalls.sol";
import { Types } from "src/libraries/Types.sol";
import { IProxyAdmin } from "interfaces/universal/IProxyAdmin.sol";
import { ICrossDomainMessenger } from "interfaces/universal/ICrossDomainMessenger.sol";
import { IStandardBridge } from "interfaces/universal/IStandardBridge.sol";
import { IL2StandardBridge } from "interfaces/L2/IL2StandardBridge.sol";
import { IOptimismMintableERC20Factory } from "interfaces/universal/IOptimismMintableERC20Factory.sol";
import { IFeeVault } from "interfaces/L2/IFeeVault.sol";

contract SafeRollupGenesis_Test is Test {
    address gateway = makeAddr("gateway");
    address owner = makeAddr("owner");
    address recipient = makeAddr("recipient");

    function setUp() public {
        SafeRollupGenesis genesis = new SafeRollupGenesis();
        genesis.genesis(
            SafeRollupGenesis.Input({
                l2ChainID: 901,
                l1Gateway: gateway,
                proxyAdminOwner: owner,
                feeVaultRecipient: recipient
            })
        );
    }

    function test_proxyAdmin_succeeds() external view {
        IProxyAdmin admin = IProxyAdmin(Predeploys.PROXY_ADMIN);
        assertEq(admin.owner(), owner);
        assertEq(
            admin.getProxyImplementation(Predeploys.L2_STANDARD_BRIDGE),
            Predeploys.predeployToCodeNamespace(Predeploys.L2_STANDARD_BRIDGE)
        );
    }

    function test_bridging_succeeds() external view {
        assertEq(address(ICrossDomainMessenger(Predeploys.L2_CROSS_DOMAIN_MESSENGER).otherMessenger()), gateway);
        assertEq(address(IStandardBridge(payable(Predeploys.L2_STANDARD_BRIDGE)).otherBridge()), gateway);
        assertEq(
            IOptimismMintableERC20Factory(Predeploys.OPTIMISM_MINTABLE_ERC20_FACTORY).bridge(),
            Predeploys.L2_STANDARD_BRIDGE
        );
    }

    function test_feeVaults_succeeds() external view {
        address[4] memory vaults = [
            Predeploys.SEQUENCER_FEE_WALLET,
            Predeploys.BASE_FEE_VAULT,
            Predeploys.L1_FEE_VAULT,
            Predeploys.OPERATOR_FEE_VAULT
        ];
        for (uint256 i; i < vaults.length; i++) {
            assertEq(IFeeVault(payable(vaults[i])).recipient(), recipient);
            assertEq(uint8(IFeeVault(payable(vaults[i])).withdrawalNetwork()), uint8(Types.WithdrawalNetwork.L2));
        }
    }

    function test_implementationsLocked_reverts() external {
        address impl = Predeploys.predeployToCodeNamespace(Predeploys.L2_STANDARD_BRIDGE);
        vm.prank(Predeploys.PROXY_ADMIN);
        vm.expectRevert();
        IL2StandardBridge(payable(impl)).initialize(IStandardBridge(payable(gateway)));
    }

    function test_codeInstalled_succeeds() external view {
        assertGt(Predeploys.WETH.code.length, 0);
        assertGt(Predeploys.L1_BLOCK_ATTRIBUTES.code.length, 0);
        assertGt(Predeploys.L2_TO_L1_MESSAGE_PASSER.code.length, 0);
        assertGt(Predeploys.GAS_PRICE_ORACLE.code.length, 0);
        assertGt(Preinstalls.BeaconBlockRoots.code.length, 0);
        assertGt(Preinstalls.MultiCall3.code.length, 0);
    }
}
