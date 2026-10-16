// Cold side. Checks every queued payout against an L2 node that derives the chain from L1 only,
// and has the Safe owners veto anything it cannot match before the delay ends. In production this
// would page the owners; on the devnet it holds their keys.
import {
  type Address,
  type Hash,
  type Hex,
  concat,
  createPublicClient,
  createWalletClient,
  encodeFunctionData,
  getAddress,
  http,
  parseAbi,
  zeroAddress,
} from 'viem'
import { privateKeyToAccount } from 'viem/accounts'
import { contracts as c, keys, l1Chain, log, rpc, sleep } from './env.ts'
import {
  L2_TO_L1_MESSAGE_PASSER,
  type Withdrawal,
  erc20Deposits,
  getWithdrawals,
  messagePassedEvent,
  moduleAbi,
  pairKey,
  toPayout,
} from './lib.ts'

const VETO_MARGIN_SECONDS = 10n

const safeAbi = parseAbi([
  'function nonce() view returns (uint256)',
  'function getTransactionHash(address to, uint256 value, bytes data, uint8 operation, uint256 safeTxGas, uint256 baseGas, uint256 gasPrice, address gasToken, address refundReceiver, uint256 nonce) view returns (bytes32)',
  'function execTransaction(address to, uint256 value, bytes data, uint8 operation, uint256 safeTxGas, uint256 baseGas, uint256 gasPrice, address gasToken, address refundReceiver, bytes signatures) payable returns (bool)',
])

const l1 = createPublicClient({ chain: l1Chain, transport: http(rpc.l1) })
const l2 = createPublicClient({ transport: http(rpc.l2Verifier) })
const owners = [keys.OWNER1_KEY, keys.OWNER2_KEY]
  .map((key) => privateKeyToAccount(key))
  .sort((a, b) => (BigInt(a.address) < BigInt(b.address) ? -1 : 1))
const submitter = createWalletClient({ account: owners[0], chain: l1Chain, transport: http() })

const withdrawals = new Map<Hash, Withdrawal>()
const spent = new Map<string, bigint>()
type Queued = { hash: Hash; to: Address; value: bigint; data: Hex; queuedAt: bigint }
let unchecked: Queued[] = []
let nextL1Block = 0n
let nextL2Block = 0n

async function veto(hash: Hash, reason: string) {
  const data = encodeFunctionData({ abi: moduleAbi, functionName: 'veto', args: [hash] })
  const nonce = await l1.readContract({ address: c.safe, abi: safeAbi, functionName: 'nonce' })
  const safeTxHash = await l1.readContract({
    address: c.safe,
    abi: safeAbi,
    functionName: 'getTransactionHash',
    args: [c.module, 0n, data, 0, 0n, 0n, 0n, zeroAddress, zeroAddress, nonce],
  })
  const signatures = concat(await Promise.all(owners.map((owner) => owner.sign({ hash: safeTxHash }))))
  const tx = await submitter.writeContract({
    address: c.safe,
    abi: safeAbi,
    functionName: 'execTransaction',
    args: [c.module, 0n, data, 0, 0n, 0n, 0n, zeroAddress, zeroAddress, signatures],
  })
  await l1.waitForTransactionReceipt({ hash: tx })
  log('watcher', `VETOED: ${reason}`, { hash })
}

/** Returns false while the withdrawal is not visible yet on the verifier node. */
async function check(q: Queued): Promise<boolean> {
  const w = withdrawals.get(q.hash)
  if (!w) {
    const { timestamp } = await l1.getBlock()
    const delay = await l1.readContract({ address: c.module, abi: moduleAbi, functionName: 'delay' })
    if (timestamp + VETO_MARGIN_SECONDS < q.queuedAt + delay) return false
    await veto(q.hash, 'no such withdrawal on L2')
    return true
  }
  const expected = await toPayout(w, c, l1)
  if (!expected || getAddress(expected.to) !== getAddress(q.to) || expected.value !== q.value || expected.data !== q.data) {
    await veto(q.hash, 'payout does not match the L2 withdrawal')
    return true
  }
  if (expected.erc20) {
    const key = pairKey(expected.erc20.l1Token, expected.erc20.l2Token)
    const used = (spent.get(key) ?? 0n) + expected.erc20.amount
    if (used > ((await erc20Deposits(l1, c)).get(key) ?? 0n)) {
      await veto(q.hash, 'token payout above deposits')
      return true
    }
    spent.set(key, used)
  }
  log('watcher', 'payout matches L2 withdrawal', { hash: q.hash })
  return true
}

while (true) {
  const l2Head = await l2.getBlockNumber()
  if (l2Head >= nextL2Block) {
    const logs = await l2.getLogs({
      address: L2_TO_L1_MESSAGE_PASSER,
      event: messagePassedEvent,
      fromBlock: nextL2Block,
      toBlock: l2Head,
    })
    for (const w of getWithdrawals(logs)) withdrawals.set(w.withdrawalHash, w)
    nextL2Block = l2Head + 1n
  }

  const l1Head = await l1.getBlockNumber()
  if (l1Head >= nextL1Block) {
    const events = await l1.getContractEvents({
      address: c.module,
      abi: moduleAbi,
      eventName: 'Queued',
      fromBlock: nextL1Block,
      toBlock: l1Head,
    })
    for (const e of events) {
      const block = await l1.getBlock({ blockNumber: e.blockNumber })
      const { withdrawalHash, to, value, data } = e.args as Required<typeof e.args>
      unchecked.push({ hash: withdrawalHash, to, value, data, queuedAt: block.timestamp })
    }
    nextL1Block = l1Head + 1n
  }

  const stillUnchecked: Queued[] = []
  for (const q of unchecked) if (!(await check(q))) stillUnchecked.push(q)
  unchecked = stillUnchecked

  await sleep(2000)
}
