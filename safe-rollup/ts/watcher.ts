// Cold side. Checks every queued payout against an L2 node that derives the chain from L1 only,
// and has the Safe owners veto anything it cannot match before the delay ends. In production this
// would page the owners; on the devnet it holds their keys.
import {
  type Address,
  type Hash,
  type Hex,
  createPublicClient,
  createWalletClient,
  getAddress,
  http,
} from 'viem'
import { privateKeyToAccount } from 'viem/accounts'
import { contracts as c, config, keys, l1Chain, l1Transport, log, rpc, sleep } from './env.ts'
import { review } from './compliance.ts'
import {
  L1_LAG,
  L2_TO_L1_MESSAGE_PASSER,
  type Withdrawal,
  erc20Deposits,
  execSafe,
  getWithdrawals,
  inRanges,
  messagePassedEvent,
  moduleAbi,
  pairKey,
  toPayout,
  vetoData,
} from './lib.ts'


const l1 = createPublicClient({ chain: l1Chain, transport: l1Transport })
const l2 = createPublicClient({ transport: http(rpc.l2Verifier) })
// The first owner key pays for the vetoes, so it is the one to fund.
const owners = [keys.OWNER1_KEY, keys.OWNER2_KEY].map((key) => privateKeyToAccount(key))
const submitter = createWalletClient({
  account: privateKeyToAccount(keys.OWNER1_KEY),
  chain: l1Chain,
  transport: http(),
})

const withdrawals = new Map<Hash, Withdrawal>()
const spent = new Map<string, bigint>()
type Queued = { hash: Hash; to: Address; value: bigint; data: Hex; queuedAt: bigint }
let unchecked: Queued[] = []
let nextL1Block = c.deployBlock
let nextL2Block = 0n

/** Vetoes a payout. The reason is recorded onchain, in the Safe transaction. */
async function veto(hash: Hash, reason: string) {
  const tx = await execSafe(l1, submitter, c.safe, owners, c.module, vetoData(hash, reason))
  await l1.waitForTransactionReceipt({ hash: tx })
  log('watcher', `VETOED: ${reason}`, { hash })
}

/** Returns false while the withdrawal is not visible yet on the verifier node. */
async function check(q: Queued): Promise<boolean> {
  // After a restart the scan replays every Queued event: skip vetoed payouts, and never veto paid ones.
  const [, queuedAt, executed] = await l1.readContract({
    address: c.module,
    abi: moduleAbi,
    functionName: 'items',
    args: [q.hash],
  })
  if (queuedAt === 0n) return true
  const reject = async (reason: string) => {
    if (executed) log('watcher', `EXECUTED BEFORE IT WAS CHECKED: ${reason}`, { hash: q.hash })
    else await veto(q.hash, reason)
    return true
  }
  const w = withdrawals.get(q.hash)
  if (!w) {
    const { timestamp } = await l1.getBlock()
    const delay = await l1.readContract({ address: c.module, abi: moduleAbi, functionName: 'delay' })
    if (!executed && timestamp + config.vetoMarginSeconds < q.queuedAt + delay) return false
    return reject('INT-01 There is no such withdrawal on L2.')
  }
  const expected = await toPayout(w, c, l1)
  if (!expected || getAddress(expected.to) !== getAddress(q.to) || expected.value !== q.value || expected.data !== q.data) {
    return reject('INT-02 The payout does not match the L2 withdrawal.')
  }
  if (expected.erc20) {
    const key = pairKey(expected.erc20.l1Token, expected.erc20.l2Token)
    const used = (spent.get(key) ?? 0n) + expected.erc20.amount
    if (used > ((await erc20Deposits(l1, c)).get(key) ?? 0n)) return reject('INT-03 The token payout exceeds deposits.')
  }
  const finding = executed ? undefined : review(q.hash, expected)
  if (finding) {
    await veto(q.hash, finding)
    return true
  }
  if (expected.erc20) {
    const key = pairKey(expected.erc20.l1Token, expected.erc20.l2Token)
    spent.set(key, (spent.get(key) ?? 0n) + expected.erc20.amount)
  }
  log('watcher', 'payout matches L2 withdrawal', { hash: q.hash })
  return true
}

async function tick() {
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

  const l1Head = (await l1.getBlockNumber()) - L1_LAG
  if (l1Head >= nextL1Block) {
    const events = await inRanges(
      (fromBlock, toBlock) =>
        l1.getContractEvents({ address: c.module, abi: moduleAbi, eventName: 'Queued', fromBlock, toBlock }),
      nextL1Block,
      l1Head,
    )
    for (const e of events) {
      const block = await l1.getBlock({ blockNumber: e.blockNumber })
      const { withdrawalHash, to, value, data } = e.args as Required<typeof e.args>
      if (unchecked.some((q) => q.hash === withdrawalHash)) continue
      unchecked.push({ hash: withdrawalHash, to, value, data, queuedAt: block.timestamp })
    }
    nextL1Block = l1Head + 1n
  }

  const stillUnchecked: Queued[] = []
  for (const q of unchecked) if (!(await check(q))) stillUnchecked.push(q)
  unchecked = stillUnchecked
}

// An RPC error must not take the watcher down: its state survives and the next tick retries.
while (true) {
  try {
    await tick()
  } catch (e) {
    log('watcher', 'tick failed, retrying', { error: (e as Error).message.split('\n')[0] })
  }
  await sleep(config.pollMs)
}
