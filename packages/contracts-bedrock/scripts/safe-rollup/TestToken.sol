// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { ERC20 } from "@openzeppelin/contracts/token/ERC20/ERC20.sol";

/// @notice Plain L1 token for the devnet demo.
contract TestToken is ERC20 {
    constructor(address _holder) ERC20("Test Token", "TEST") {
        _mint(_holder, 1_000_000 ether);
    }
}
