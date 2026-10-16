// Client library for a Safe rollup: deposits, withdrawals and their status, plus the rule that maps
// an L2 withdrawal to the Safe call paying it out, shared by the relayer and the watcher.
import {
  type Address,
  type Hash,
  type Hex,
  type Log,
  type PublicClient,
  type WalletClient,
  decodeEventLog,
  decodeFunctionData,
  encodeFunctionData,
  getAddress,
  parseAbi,
  sliceHex,
  toFunctionSelector,
} from 'viem'

export const L2_CROSS_DOMAIN_MESSENGER = '0x4200000000000000000000000000000000000007'
export const L2_STANDARD_BRIDGE = '0x4200000000000000000000000000000000000010'
export const L2_TO_L1_MESSAGE_PASSER = '0x4200000000000000000000000000000000000016'

export const gatewayAbi = parseAbi([
  'function depositTransaction(address to, uint256 value, uint64 gasLimit, bool isCreation, bytes data) payable',
  'function sendMessage(address target, bytes message, uint32 minGasLimit) payable',
  'function depositERC20To(address l1Token, address l2Token, address to, uint256 amount, uint32 minGasLimit, bytes extraData)',
  'function relayMessage(address sender, address target, bytes message) payable',
  'function mintERC20(address l1Token, address to, uint256 amount)',
  'event TransactionDeposited(address indexed from, address indexed to, uint256 indexed version, bytes opaqueData)',
])
export const moduleAbi = parseAbi([
  'function queue(bytes32 withdrawalHash, address to, uint256 value, bytes data)',
  'function execute(bytes32 withdrawalHash, address to, uint256 value, bytes data)',
  'function veto(bytes32 withdrawalHash)',
  'function items(bytes32) view returns (bytes32 callHash, uint64 queuedAt, bool executed)',
  'function delay() view returns (uint64)',
  'event Queued(bytes32 indexed withdrawalHash, address to, uint256 value, bytes data)',
  'event Vetoed(bytes32 indexed withdrawalHash)',
  'event Executed(bytes32 indexed withdrawalHash)',
])
export const messagePassedEvent = parseAbi([
  'event MessagePassed(uint256 indexed nonce, address indexed sender, address indexed target, uint256 value, uint256 gasLimit, bytes data, bytes32 withdrawalHash)',
])[0]
const messengerAbi = parseAbi([
  'function relayMessage(uint256 nonce, address sender, address target, uint256 value, uint256 minGasLimit, bytes message)',
])
const bridgeAbi = parseAbi([
  'function finalizeBridgeETH(address from, address to, uint256 amount, bytes extraData)',
  'function finalizeBridgeERC20(address localToken, address remoteToken, address from, address to, uint256 amount, bytes extraData)',
  'function bridgeETHTo(address to, uint32 minGasLimit, bytes extraData) payable',
  'function bridgeERC20To(address localToken, address remoteToken, address to, uint256 amount, uint32 minGasLimit, bytes extraData)',
])
const mintableAbi = parseAbi([
  'function supportsInterface(bytes4 interfaceId) view returns (bool)',
  'function remoteToken() view returns (address)',
  'function bridge() view returns (address)',
])
const mintableInterfaceId = toHex4(
  ['remoteToken()', 'bridge()', 'mint(address,uint256)', 'burn(address,uint256)']
    .map((sig) => BigInt(toFunctionSelector(sig)))
    .reduce((a, b) => a ^ b),
)

export const erc20Abi = parseAbi([
  'function approve(address spender, uint256 amount) returns (bool)',
  'function transfer(address to, uint256 amount) returns (bool)',
  'function balanceOf(address owner) view returns (uint256)',
])

export type Contracts = { safe: Address; gateway: Address; module: Address }

export type Withdrawal = {
  withdrawalHash: Hash
  nonce: bigint
  sender: Address
  target: Address
  value: bigint
  gasLimit: bigint
  data: Hex
}

/** The Safe call that pays out a withdrawal. `erc20` is set for payouts from tokens escrowed in the Safe. */
export type Payout = {
  to: Address
  value: bigint
  data: Hex
  erc20?: { l1Token: Address; l2Token: Address; amount: bigint }
}

// Deposits.

export function depositETH(l1: WalletClient, c: Contracts, to: Address, amount: bigint) {
  return l1.writeContract({
    address: c.gateway,
    abi: gatewayAbi,
    functionName: 'depositTransaction',
    args: [to, amount, 100_000n, false, '0x'],
    value: amount,
    account: l1.account!,
    chain: l1.chain,
  })
}

/** L1-native tokens need an allowance of `amount` for the gateway. L1 copies of L2-native tokens are burned. */
export function depositERC20(
  l1: WalletClient,
  c: Contracts,
  l1Token: Address,
  l2Token: Address,
  to: Address,
  amount: bigint,
) {
  return l1.writeContract({
    address: c.gateway,
    abi: gatewayAbi,
    functionName: 'depositERC20To',
    args: [l1Token, l2Token, to, amount, 200_000, '0x'],
    account: l1.account!,
    chain: l1.chain,
  })
}

// Withdrawals: initiated on L2 with the standard predeploys, paid out on L1 by the Safe.

export function withdrawETH(l2: WalletClient, to: Address, amount: bigint) {
  return l2.writeContract({
    address: L2_STANDARD_BRIDGE,
    abi: bridgeAbi,
    functionName: 'bridgeETHTo',
    args: [to, 0, '0x'],
    value: amount,
    account: l2.account!,
    chain: l2.chain,
  })
}

/** L2-native tokens need an allowance of `amount` for the L2 standard bridge. */
export function withdrawERC20(l2: WalletClient, l2Token: Address, l1Token: Address, to: Address, amount: bigint) {
  return l2.writeContract({
    address: L2_STANDARD_BRIDGE,
    abi: bridgeAbi,
    functionName: 'bridgeERC20To',
    args: [l2Token, l1Token, to, amount, 0, '0x'],
    account: l2.account!,
    chain: l2.chain,
  })
}

export function getWithdrawals(logs: Log[]): Withdrawal[] {
  return logs
    .filter((log) => getAddress(log.address) === L2_TO_L1_MESSAGE_PASSER)
    .flatMap((log) => {
      try {
        const { args } = decodeEventLog({ abi: [messagePassedEvent], ...log })
        return [{ ...args, sender: getAddress(args.sender), target: getAddress(args.target) }]
      } catch {
        return []
      }
    })
}

export type WithdrawalStatus =
  | { status: 'waiting-to-queue' }
  | { status: 'queued'; executableAt: bigint }
  | { status: 'ready-to-execute' }
  | { status: 'executed' }

export async function getWithdrawalStatus(
  l1: PublicClient,
  c: Contracts,
  withdrawalHash: Hash,
): Promise<WithdrawalStatus> {
  const [, queuedAt, executed] = await l1.readContract({
    address: c.module,
    abi: moduleAbi,
    functionName: 'items',
    args: [withdrawalHash],
  })
  if (executed) return { status: 'executed' }
  if (queuedAt === 0n) return { status: 'waiting-to-queue' }
  const delay = await l1.readContract({ address: c.module, abi: moduleAbi, functionName: 'delay' })
  const executableAt = queuedAt + delay
  const { timestamp } = await l1.getBlock()
  return timestamp >= executableAt ? { status: 'ready-to-execute' } : { status: 'queued', executableAt }
}

/** Anyone can execute a payout once its delay has passed. */
export function executeWithdrawal(l1: WalletClient, c: Contracts, withdrawalHash: Hash, payout: Payout) {
  return l1.writeContract({
    address: c.module,
    abi: moduleAbi,
    functionName: 'execute',
    args: [withdrawalHash, payout.to, payout.value, payout.data],
    account: l1.account!,
    chain: l1.chain,
  })
}

// Payout rule. Returns undefined for withdrawals that must never be paid out.

export async function toPayout(w: Withdrawal, c: Contracts, l1: PublicClient): Promise<Payout | undefined> {
  const forbidden = [c.gateway, c.safe, c.module].map((a) => getAddress(a))
  const relay = (sender: Address, target: Address, value: bigint, message: Hex) =>
    forbidden.includes(getAddress(target))
      ? undefined
      : {
          to: c.gateway,
          value,
          data: encodeFunctionData({ abi: gatewayAbi, functionName: 'relayMessage', args: [sender, target, message] }),
        }

  if (w.sender !== L2_CROSS_DOMAIN_MESSENGER) return relay(w.sender, w.target, w.value, w.data)
  if (w.target !== getAddress(c.gateway)) return undefined

  const { args } = decodeFunctionData({ abi: messengerAbi, data: w.data })
  const [, sender, target, value, , message] = args
  if (value !== w.value) return undefined
  if (getAddress(sender) !== L2_STANDARD_BRIDGE || getAddress(target) !== getAddress(c.gateway)) {
    return relay(sender, target, value, message)
  }

  const call = decodeFunctionData({ abi: bridgeAbi, data: message })
  if (call.functionName === 'finalizeBridgeETH') {
    const [, to, amount] = call.args
    if (amount !== value || forbidden.includes(getAddress(to))) return undefined
    return { to, value: amount, data: '0x' }
  }
  if (call.functionName === 'finalizeBridgeERC20' && value === 0n) {
    const [l1Token, l2Token, , to, amount] = call.args
    if (await isL1CopyOf(l1, c, l1Token, l2Token)) {
      return {
        to: c.gateway,
        value: 0n,
        data: encodeFunctionData({ abi: gatewayAbi, functionName: 'mintERC20', args: [l1Token, to, amount] }),
      }
    }
    return {
      to: l1Token,
      value: 0n,
      data: encodeFunctionData({ abi: erc20Abi, functionName: 'transfer', args: [to, amount] }),
      erc20: { l1Token, l2Token, amount },
    }
  }
  return undefined
}

/** Whether `l1Token` is the L1 copy of the L2-native `l2Token`, minted and burned by the gateway. */
async function isL1CopyOf(l1: PublicClient, c: Contracts, l1Token: Address, l2Token: Address) {
  try {
    const [isMintable, remote, bridge] = await Promise.all([
      l1.readContract({ address: l1Token, abi: mintableAbi, functionName: 'supportsInterface', args: [mintableInterfaceId] }),
      l1.readContract({ address: l1Token, abi: mintableAbi, functionName: 'remoteToken' }),
      l1.readContract({ address: l1Token, abi: mintableAbi, functionName: 'bridge' }),
    ])
    return isMintable && getAddress(remote) === getAddress(l2Token) && getAddress(bridge) === getAddress(c.gateway)
  } catch {
    return false
  }
}

function toHex4(n: bigint): Hex {
  return `0x${n.toString(16).padStart(8, '0')}`
}

/** ERC20 amounts deposited through the gateway per (L1 token, L2 token) pair. */
export async function erc20Deposits(l1: PublicClient, c: Contracts): Promise<Map<string, bigint>> {
  const logs = await l1.getContractEvents({
    address: c.gateway,
    abi: gatewayAbi,
    eventName: 'TransactionDeposited',
    args: { to: L2_CROSS_DOMAIN_MESSENGER },
    fromBlock: 0n,
  })
  const deposits = new Map<string, bigint>()
  for (const log of logs) {
    // opaqueData is abi.encodePacked(mint, value, gasLimit, isCreation, data).
    const data = sliceHex(log.args.opaqueData!, 73)
    const { args } = decodeFunctionData({ abi: messengerAbi, data })
    const [, sender, target, , , message] = args
    if (getAddress(sender) !== getAddress(c.gateway) || getAddress(target) !== L2_STANDARD_BRIDGE) continue
    const call = decodeFunctionData({ abi: bridgeAbi, data: message })
    if (call.functionName !== 'finalizeBridgeERC20') continue
    const [l2Token, l1Token, , , amount] = call.args
    const key = pairKey(l1Token, l2Token)
    deposits.set(key, (deposits.get(key) ?? 0n) + amount)
  }
  return deposits
}

export function pairKey(l1Token: Address, l2Token: Address) {
  return `${getAddress(l1Token)}:${getAddress(l2Token)}`
}
