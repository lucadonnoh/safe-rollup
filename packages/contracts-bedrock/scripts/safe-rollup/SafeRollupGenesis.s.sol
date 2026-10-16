// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { Script } from "forge-std/Script.sol";
import { SetPreinstalls } from "scripts/SetPreinstalls.s.sol";
import { Predeploys } from "src/libraries/Predeploys.sol";
import { AddressAliasHelper } from "src/vendor/AddressAliasHelper.sol";
import { Types } from "src/libraries/Types.sol";
import { IProxy } from "interfaces/universal/IProxy.sol";
import { ICrossDomainMessenger } from "interfaces/universal/ICrossDomainMessenger.sol";
import { IStandardBridge } from "interfaces/universal/IStandardBridge.sol";
import { IOptimismMintableERC20Factory } from "interfaces/universal/IOptimismMintableERC20Factory.sol";
import { IL2CrossDomainMessenger } from "interfaces/L2/IL2CrossDomainMessenger.sol";
import { IL2StandardBridge } from "interfaces/L2/IL2StandardBridge.sol";
import { IFeeVault } from "interfaces/L2/IFeeVault.sol";

/// @title SafeRollupGenesis
/// @notice L2 genesis state of a Safe rollup at Jovian: the predeploys the chain uses, initialized
///         against the SafeRollupGateway, plus the standard preinstalls. L2 contracts are upgraded
///         by the Safe through deposits from its L2 alias, which owns the L2ProxyAdmin.
contract SafeRollupGenesis is Script {
    struct Input {
        uint256 l2ChainID;
        address l1Gateway;
        address proxyAdminOwner;
        address feeVaultRecipient;
    }

    bytes32 internal constant EIP1967_IMPLEMENTATION_SLOT =
        0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc;
    bytes32 internal constant EIP1967_ADMIN_SLOT = 0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103;
    uint256 internal constant FEE_VAULT_MIN_WITHDRAWAL = 10 ether;

    /// @notice Writes the genesis allocs to `OUT`, reading the input from the environment. The Safe
    ///         owns the L2 ProxyAdmin through its alias and receives the fees.
    function run() external {
        genesis(
            Input({
                l2ChainID: vm.envUint("L2_CHAIN_ID"),
                l1Gateway: vm.envAddress("GATEWAY"),
                proxyAdminOwner: AddressAliasHelper.applyL1ToL2Alias(vm.envAddress("SAFE")),
                feeVaultRecipient: vm.envAddress("SAFE")
            })
        );
        // Leave only the L2 state in the dump.
        vm.deal(msg.sender, 0);
        vm.resetNonce(msg.sender);
        vm.dumpState(vm.envString("OUT"));
    }

    function genesis(Input memory _input) public {
        vm.chainId(_input.l2ChainID);
        for (uint256 i; i < 256; i++) {
            vm.deal(address(uint160(i)), 1);
        }
        setPreinstalls();

        // The ProxyAdmin is its own proxy's admin, and its owner must be set before any initializer
        // checks it.
        setProxy(Predeploys.PROXY_ADMIN);
        vm.store(Predeploys.PROXY_ADMIN, EIP1967_IMPLEMENTATION_SLOT, toBytes32(code("ProxyAdmin", Predeploys.PROXY_ADMIN)));
        vm.store(Predeploys.PROXY_ADMIN, bytes32(0), toBytes32(_input.proxyAdminOwner));

        initialize(
            Predeploys.L2_CROSS_DOMAIN_MESSENGER,
            "L2CrossDomainMessenger",
            abi.encodeCall(IL2CrossDomainMessenger.initialize, (ICrossDomainMessenger(_input.l1Gateway)))
        );
        initialize(
            Predeploys.L2_STANDARD_BRIDGE,
            "L2StandardBridge",
            abi.encodeCall(IL2StandardBridge.initialize, (IStandardBridge(payable(_input.l1Gateway))))
        );
        initialize(
            Predeploys.OPTIMISM_MINTABLE_ERC20_FACTORY,
            "OptimismMintableERC20Factory",
            abi.encodeCall(IOptimismMintableERC20Factory.initialize, (Predeploys.L2_STANDARD_BRIDGE))
        );
        bytes memory feeVaultInit = abi.encodeCall(
            IFeeVault.initialize, (_input.feeVaultRecipient, FEE_VAULT_MIN_WITHDRAWAL, Types.WithdrawalNetwork.L2)
        );
        initialize(Predeploys.SEQUENCER_FEE_WALLET, "SequencerFeeVault", feeVaultInit);
        initialize(Predeploys.BASE_FEE_VAULT, "BaseFeeVault", feeVaultInit);
        initialize(Predeploys.L1_FEE_VAULT, "L1FeeVault", feeVaultInit);
        initialize(Predeploys.OPERATOR_FEE_VAULT, "OperatorFeeVault", feeVaultInit);
        initialize(Predeploys.L1_BLOCK_ATTRIBUTES, "L1Block", "");
        initialize(Predeploys.L2_TO_L1_MESSAGE_PASSER, "L2ToL1MessagePasser", "");
        initialize(Predeploys.GAS_PRICE_ORACLE, "GasPriceOracle", "");
        vm.etch(Predeploys.WETH, vm.getDeployedCode("WETH.sol:WETH"));
    }

    /// @notice Sets the proxy and implementation code of a predeploy, then initializes the proxy
    ///         through the ProxyAdmin. An empty `_init` only points the proxy at the implementation.
    function initialize(address _predeploy, string memory _name, bytes memory _init) internal {
        setProxy(_predeploy);
        address impl = code(_name, _predeploy);
        vm.prank(Predeploys.PROXY_ADMIN);
        if (_init.length == 0) {
            IProxy(payable(_predeploy)).upgradeTo(impl);
        } else {
            IProxy(payable(_predeploy)).upgradeToAndCall(impl, _init);
            // Lock the implementation so nobody else can initialize it.
            vm.store(impl, EIP1967_ADMIN_SLOT, toBytes32(Predeploys.PROXY_ADMIN));
            vm.prank(Predeploys.PROXY_ADMIN);
            (bool success,) = impl.call(_init);
            require(success, "SafeRollupGenesis: failed to lock implementation");
        }
    }

    function setProxy(address _predeploy) internal {
        vm.etch(_predeploy, vm.getDeployedCode("Proxy.sol:Proxy"));
        vm.store(_predeploy, EIP1967_ADMIN_SLOT, toBytes32(Predeploys.PROXY_ADMIN));
    }

    /// @notice Places the implementation code at the predeploy's code namespace address.
    function code(string memory _name, address _proxy) internal returns (address impl_) {
        impl_ = Predeploys.predeployToCodeNamespace(_proxy);
        vm.etch(impl_, vm.getDeployedCode(string.concat(_name, ".sol:", _name)));
    }

    function setPreinstalls() internal {
        address tmp = address(uint160(uint256(keccak256("SetPreinstalls"))));
        vm.etch(tmp, vm.getDeployedCode("SetPreinstalls.s.sol:SetPreinstalls"));
        SetPreinstalls(tmp).setPreinstalls();
        vm.etch(tmp, "");
        vm.resetNonce(tmp);
    }

    function toBytes32(address _addr) internal pure returns (bytes32) {
        return bytes32(uint256(uint160(_addr)));
    }
}
