// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { Safe } from "safe-contracts/Safe.sol";
import { Enum } from "safe-contracts/common/Enum.sol";

/// @title WithdrawalDelayModule
/// @notice Safe module that pays out L2 withdrawals. A hot relayer key queues one Safe call per L2
///         withdrawal hash. After the delay anyone can execute it, unless the Safe vetoed it first.
///         Nothing onchain checks that a queued call matches an L2 withdrawal: that is the job of
///         the Safe owners' watcher, which vetoes anything it cannot match against its own L2 node.
contract WithdrawalDelayModule {
    struct Item {
        bytes32 callHash;
        uint64 queuedAt;
        bool executed;
    }

    Safe public immutable safe;
    address public relayer;
    uint64 public delay;

    /// @notice Queued payouts, keyed by L2 withdrawal hash.
    mapping(bytes32 => Item) public items;

    event Queued(bytes32 indexed withdrawalHash, address to, uint256 value, bytes data);
    event Vetoed(bytes32 indexed withdrawalHash);
    event Executed(bytes32 indexed withdrawalHash);
    event RelayerSet(address relayer);
    event DelaySet(uint64 delay);

    error Unauthorized();
    error AlreadyQueued();
    error UnsafeTarget();
    error NotQueued();
    error AlreadyExecuted();
    error TooEarly();
    error WrongCall();
    error CallFailed();

    constructor(Safe _safe, address _relayer, uint64 _delay) {
        safe = _safe;
        relayer = _relayer;
        delay = _delay;
    }

    modifier onlySafe() {
        if (msg.sender != address(safe)) revert Unauthorized();
        _;
    }

    function queue(bytes32 _withdrawalHash, address _to, uint256 _value, bytes calldata _data) external {
        if (msg.sender != relayer) revert Unauthorized();
        if (items[_withdrawalHash].queuedAt != 0) revert AlreadyQueued();
        // A call from the Safe to itself or to this module could change owners or modules.
        if (_to == address(safe) || _to == address(this)) revert UnsafeTarget();

        items[_withdrawalHash] = Item({
            callHash: keccak256(abi.encode(_to, _value, _data)),
            queuedAt: uint64(block.timestamp),
            executed: false
        });
        emit Queued(_withdrawalHash, _to, _value, _data);
    }

    /// @notice Drops a queued payout. The relayer can queue the withdrawal again with a fixed call.
    function veto(bytes32 _withdrawalHash) external onlySafe {
        if (items[_withdrawalHash].executed) revert AlreadyExecuted();
        delete items[_withdrawalHash];
        emit Vetoed(_withdrawalHash);
    }

    /// @notice Executes a queued payout. A failed call reverts, so it can be retried later.
    function execute(bytes32 _withdrawalHash, address _to, uint256 _value, bytes calldata _data) external {
        Item storage item = items[_withdrawalHash];
        if (item.queuedAt == 0) revert NotQueued();
        if (item.executed) revert AlreadyExecuted();
        if (block.timestamp < item.queuedAt + delay) revert TooEarly();
        if (item.callHash != keccak256(abi.encode(_to, _value, _data))) revert WrongCall();

        item.executed = true;
        if (!safe.execTransactionFromModule(_to, _value, _data, Enum.Operation.Call)) revert CallFailed();
        emit Executed(_withdrawalHash);
    }

    function setRelayer(address _relayer) external onlySafe {
        relayer = _relayer;
        emit RelayerSet(_relayer);
    }

    function setDelay(uint64 _delay) external onlySafe {
        delay = _delay;
        emit DelaySet(_delay);
    }
}
