// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { Safe } from "safe-contracts/Safe.sol";
import { Enum } from "safe-contracts/common/Enum.sol";

/// @title WithdrawalDelayModule
/// @notice Safe module that pays out L2 withdrawals. A hot relayer key queues one Safe call per L2
///         withdrawal hash. After the delay anyone can execute it, unless the Safe vetoed it first.
///         Nothing onchain checks that a queued call matches an L2 withdrawal: that is the job of
///         the Safe owners' watcher, which vetoes anything it cannot match against its own L2 node.
///         The relayer and the delay are fixed: the Safe changes them by swapping modules.
contract WithdrawalDelayModule {
    struct Item {
        bytes32 callHash;
        uint64 queuedAt;
        bool executed;
    }

    Safe public immutable safe;
    address public immutable relayer;
    uint64 public immutable delay;

    /// @notice Queued payouts, keyed by L2 withdrawal hash.
    mapping(bytes32 => Item) public items;

    event Queued(bytes32 indexed withdrawalHash, address to, uint256 value, bytes data);
    event Vetoed(bytes32 indexed withdrawalHash);
    event Executed(bytes32 indexed withdrawalHash);

    error Unauthorized();
    error UnsafeTarget();
    error NotExecutable();

    constructor(Safe _safe, address _relayer, uint64 _delay) {
        safe = _safe;
        relayer = _relayer;
        delay = _delay;
    }

    function queue(bytes32 _withdrawalHash, address _to, uint256 _value, bytes calldata _data) external {
        if (msg.sender != relayer || items[_withdrawalHash].queuedAt != 0) revert Unauthorized();
        // A call from the Safe to itself or to this module could change owners or modules.
        if (_to == address(safe) || _to == address(this)) revert UnsafeTarget();

        items[_withdrawalHash] = Item(keccak256(abi.encode(_to, _value, _data)), uint64(block.timestamp), false);
        emit Queued(_withdrawalHash, _to, _value, _data);
    }

    /// @notice Drops a queued payout. The relayer can queue the withdrawal again with a fixed call.
    function veto(bytes32 _withdrawalHash) external {
        if (msg.sender != address(safe) || items[_withdrawalHash].executed) revert Unauthorized();
        delete items[_withdrawalHash];
        emit Vetoed(_withdrawalHash);
    }

    /// @notice Executes a queued payout after the delay. A failed call reverts, so it can be
    ///         retried later.
    function execute(bytes32 _withdrawalHash, address _to, uint256 _value, bytes calldata _data) external {
        Item storage item = items[_withdrawalHash];
        if (item.queuedAt == 0 || item.executed || block.timestamp < item.queuedAt + delay) revert NotExecutable();
        if (item.callHash != keccak256(abi.encode(_to, _value, _data))) revert NotExecutable();

        item.executed = true;
        if (!safe.execTransactionFromModule(_to, _value, _data, Enum.Operation.Call)) revert NotExecutable();
        emit Executed(_withdrawalHash);
    }
}
