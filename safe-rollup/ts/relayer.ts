// Hot key. Queues the Safe payout of every L2 withdrawal once its L2 block is safe (derived from
// L1), then executes payouts whose delay has passed. Replaces op-proposer: no state roots on L1.
import { type Hash, createPublicClient, createWalletClient, http } from 'viem'
import { privateKeyToAccount } from 'viem/accounts'
import { contracts as c, keys, l1Chain, log, rpc, safeL2Block, sleep } from './env.ts'
import {
  L2_TO_L1_MESSAGE_PASSER,
  type Payout,
  erc20Deposits,
  executeWithdrawal,
  getWithdrawalStatus,
  getWithdrawals,
  messagePassedEvent,
  moduleAbi,
  pairKey,
  toPayout,
} from './lib.ts'

const l1 = createPublicClient({ chain: l1Chain, transport: http(rpc.l1) })
const l2 = createPublicClient({ transport: http(rpc.l2) })
const relayer = createWalletClient({ account: privateKeyToAccount(keys.RELAYER_KEY), chain: l1Chain, transport: http() })

let nextBlock = 0n
const pending = new Map<Hash, Payout>()
const spent = new Map<string, bigint>()

while (true) {
  const safeHead = await safeL2Block()
  if (safeHead >= nextBlock) {
    const logs = await l2.getLogs({
      address: L2_TO_L1_MESSAGE_PASSER,
      event: messagePassedEvent,
      fromBlock: nextBlock,
      toBlock: safeHead,
    })
    const deposits = await erc20Deposits(l1, c)
    for (const w of getWithdrawals(logs)) {
      const payout = await toPayout(w, c, l1)
      if (!payout) {
        log('relayer', 'skipping withdrawal that cannot be paid out', { hash: w.withdrawalHash })
        continue
      }
      if (payout.erc20) {
        const key = pairKey(payout.erc20.l1Token, payout.erc20.l2Token)
        const used = (spent.get(key) ?? 0n) + payout.erc20.amount
        if (used > (deposits.get(key) ?? 0n)) {
          log('relayer', 'skipping token withdrawal above deposits', { hash: w.withdrawalHash })
          continue
        }
        spent.set(key, used)
      }
      const [, queuedAt] = await l1.readContract({
        address: c.module,
        abi: moduleAbi,
        functionName: 'items',
        args: [w.withdrawalHash],
      })
      if (queuedAt === 0n) {
        const tx = await relayer.writeContract({
          address: c.module,
          abi: moduleAbi,
          functionName: 'queue',
          args: [w.withdrawalHash, payout.to, payout.value, payout.data],
        })
        await l1.waitForTransactionReceipt({ hash: tx })
        log('relayer', 'queued', { hash: w.withdrawalHash, to: payout.to, value: payout.value })
      }
      pending.set(w.withdrawalHash, payout)
    }
    nextBlock = safeHead + 1n
  }

  for (const [hash, payout] of pending) {
    const { status } = await getWithdrawalStatus(l1, c, hash)
    if (status === 'ready-to-execute') {
      const tx = await executeWithdrawal(relayer, c, hash, payout)
      const receipt = await l1.waitForTransactionReceipt({ hash: tx })
      log('relayer', `executed: ${receipt.status}`, { hash })
    }
    if (status === 'ready-to-execute' || status === 'executed' || status === 'waiting-to-queue') pending.delete(hash)
  }

  await sleep(2000)
}
