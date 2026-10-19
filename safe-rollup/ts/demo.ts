// End to end run: deposits and withdrawals of ETH and ERC20s through the Safe, a forced transfer out of the
// user's L2 account, then (DEMO_ATTACK=true) a compromised relayer key trying to drain the Safe, which the
// watcher vetoes. ETH amounts are multiples of DEMO_ETH.
import { readFileSync } from 'node:fs'
import {
  type Address,
  type Hash,
  type Hex,
  createPublicClient,
  createWalletClient,
  encodeFunctionData,
  formatEther,
  http,
  keccak256,
  parseAbi,
  parseEther,
  toHex,
} from 'viem'
import { privateKeyToAccount } from 'viem/accounts'
import { contracts as c, config, keys, l1Chain, l1Transport, l2Chain, log, rpc, sleep } from './env.ts'
import {
  L2_STANDARD_BRIDGE,
  depositERC20,
  depositETH,
  erc20Abi,
  execSafe,
  forcedTransferData,
  getWithdrawalStatus,
  getWithdrawals,
  moduleAbi,
  safeAbi,
  vetoReason,
  withdrawERC20,
  withdrawETH,
} from './lib.ts'

const factoryAbi = parseAbi([
  'function createOptimismMintableERC20(address remoteToken, string name, string symbol) returns (address)',
  'event OptimismMintableERC20Created(address indexed localToken, address indexed remoteToken, address deployer)',
])

const user = privateKeyToAccount(keys.USER_KEY)
const attacker: Address = '0x000000000000000000000000000000000000bad1'
const l1 = createPublicClient({ chain: l1Chain, transport: l1Transport })
const l2 = createPublicClient({ chain: l2Chain, transport: http(rpc.l2) })
const l1Wallet = createWalletClient({ account: user, chain: l1Chain, transport: http() })
const l2Wallet = createWalletClient({ account: user, chain: l2Chain, transport: http() })
const relayerKey = createWalletClient({
  account: privateKeyToAccount(keys.RELAYER_KEY),
  chain: l1Chain,
  transport: http(),
})
const eth = (n: number) => (parseEther(config.demoEth) * BigInt(n * 1000)) / 1000n
const delay = await l1.readContract({ address: c.module, abi: moduleAbi, functionName: 'delay' })

async function until<T>(what: string, fn: () => Promise<T | undefined>, timeoutSec = 600): Promise<T> {
  const deadline = Date.now() + timeoutSec * 1000
  while (Date.now() < deadline) {
    const v = await fn()
    if (v !== undefined) return v
    await sleep(config.pollMs)
  }
  throw new Error(`timed out waiting for ${what}`)
}

const tokenBalance = (client: typeof l1 | typeof l2, token: Address, owner: Address) =>
  client.readContract({ address: token, abi: erc20Abi, functionName: 'balanceOf', args: [owner] })

/** Sends a withdrawal on L2 and returns its hash once the transaction is included. */
async function initiate(what: string, tx: Promise<Hash>): Promise<Hash> {
  const receipt = await l2.waitForTransactionReceipt({ hash: await tx })
  const [w] = getWithdrawals(receipt.logs)
  log('demo', `${what} withdrawal initiated on L2`, { hash: w.withdrawalHash })
  return w.withdrawalHash
}

/** Returns true once the payout executes, false if compliance vetoes it. */
async function waitForPayout(what: string, withdrawalHash: Hash): Promise<boolean> {
  let last = ''
  const final = await until(`${what} payout`, async () => {
    const s = await getWithdrawalStatus(l1, c, withdrawalHash)
    if (s.status !== last) log('demo', `${what} status: ${s.status}`), (last = s.status)
    return s.status === 'executed' || s.status === 'vetoed' ? s : undefined
  }, Number(delay) + 900)
  if (final.status === 'vetoed') {
    const { input } = await l1.getTransaction({ hash: final.tx })
    log('demo', `${what} withdrawal vetoed: ${vetoReason(input) ?? 'no reason given'}`)
    return false
  }
  return true
}

function loadArtifact(name: string) {
  const path = `../../packages/contracts-bedrock/forge-artifacts/${name}.sol/${name}.json`
  const artifact = JSON.parse(readFileSync(new URL(path, import.meta.url), 'utf8'))
  return { abi: artifact.abi, bytecode: artifact.bytecode.object as Hex }
}

// 1. ETH deposit.
const ethBefore = await l2.getBalance({ address: user.address })
await l1.waitForTransactionReceipt({ hash: await depositETH(l1Wallet, c, user.address, eth(2)) })
await until('ETH on L2', async () => {
  const b = await l2.getBalance({ address: user.address })
  return b >= ethBefore + eth(2) ? b : undefined
})
log('demo', `deposited ${formatEther(eth(2))} ETH`, { l2Balance: formatEther(await l2.getBalance({ address: user.address })) })

// 2. ERC20 deposit, after creating its L2 representation with the standard predeploy factory.
const deployTx = await l1Wallet.deployContract({ ...loadArtifact('TestToken'), args: [user.address] })
const l1Token = (await l1.waitForTransactionReceipt({ hash: deployTx })).contractAddress!
const createTx = await l2Wallet.writeContract({
  address: '0x4200000000000000000000000000000000000012',
  abi: factoryAbi,
  functionName: 'createOptimismMintableERC20',
  args: [l1Token, 'Real World Asset', 'RWA'],
})
const created = await l2.waitForTransactionReceipt({ hash: createTx })
const [createdLog] = await l2.getContractEvents({
  abi: factoryAbi,
  eventName: 'OptimismMintableERC20Created',
  blockHash: created.blockHash,
})
const l2Token = createdLog.args.localToken!
await l1.waitForTransactionReceipt({
  hash: await l1Wallet.writeContract({
    address: l1Token,
    abi: erc20Abi,
    functionName: 'approve',
    args: [c.gateway, parseEther('100')],
  }),
})
await l1.waitForTransactionReceipt({
  hash: await depositERC20(l1Wallet, c, l1Token, l2Token, user.address, parseEther('100')),
})
await until('tokens on L2', async () =>
  (await tokenBalance(l2, l2Token, user.address)) === parseEther('100') ? true : undefined,
)
log('demo', 'deposited 100 RWA', { safeHolds: formatEther(await tokenBalance(l1, l1Token, c.safe)) })

// 3. Forced transfer: the Safe moves 10 RWA from the user's L2 account to the Safe's address, without the
//    user's signature, through a deposit in the user's name. The reason is recorded in the Safe
//    transaction. The user submits it, since the watcher sends vetoes from the owner key. If a veto
//    takes the Safe nonce first, the call is signed again.
//    The reason rotates with the Safe nonce, which every forced transfer increments.
const FORCED_REASONS = [
  'CUS-01 Custody: assets held outside the Safe were moved into it.',
  'REC-01 Key recovery: the holder reported a lost key. Held until their identity is confirmed.',
  'CRT-01 Court order: assets restrained under a freezing order against the holder.',
  'SAN-01 Sanctions: the holder matched an entry on a sanctions list.',
  'ERR-01 Error correction: the tokens were issued to the wrong account.',
  'RED-01 Redemption: the asset matured. Proceeds are paid offchain.',
  'KYC-02 Expired verification: held in custody until the holder renews their documents.',
  'MRG-01 Margin call: the collateral fell below the maintenance requirement.',
]
const safeNonce = await l1.readContract({ address: c.safe, abi: safeAbi, functionName: 'nonce' })
const forcedReason = FORCED_REASONS[Number(safeNonce % BigInt(FORCED_REASONS.length))]
const forcedBefore = await tokenBalance(l2, l2Token, user.address)
const forced = forcedTransferData(
  user.address,
  l2Token,
  encodeFunctionData({ abi: erc20Abi, functionName: 'transfer', args: [c.safe, parseEther('10')] }),
  forcedReason,
)
const signers = [keys.OWNER1_KEY, keys.OWNER2_KEY].map((key) => privateKeyToAccount(key))
for (let attempt = 1; ; attempt++) {
  try {
    await l1.waitForTransactionReceipt({ hash: await execSafe(l1, l1Wallet, c.safe, signers, c.gateway, forced) })
    break
  } catch (e) {
    if (attempt === 3) throw e
    await sleep(config.pollMs)
  }
}
await until('forced transfer on L2', async () =>
  (await tokenBalance(l2, l2Token, user.address)) === forcedBefore - parseEther('10') ? true : undefined,
)
log('demo', `forced transfer: the Safe moved 10 RWA out of the user account (${forcedReason})`, {
  userL2Balance: formatEther(await tokenBalance(l2, l2Token, user.address)),
  safeL2Balance: formatEther(await tokenBalance(l2, l2Token, c.safe)),
})

// 4. Withdrawals of ETH, RWA and an L2-native token, initiated together and paid out in parallel. The
//    L2-native token gets an L1 copy that the Safe mints on payout. All ETH above a buffer for L2 gas is
//    withdrawn, so repeated runs do not pile up ETH on L2.
const nativeTx = await l2Wallet.deployContract({ ...loadArtifact('TestToken'), args: [user.address] })
const l2Native = (await l2.waitForTransactionReceipt({ hash: nativeTx })).contractAddress!
const copyTx = await l1Wallet.deployContract({
  ...loadArtifact('OptimismMintableERC20'),
  args: [c.safe, l2Native, 'Real World Asset on L1', 'RWA', 18],
})
const l1Copy = (await l1.waitForTransactionReceipt({ hash: copyTx })).contractAddress!
await l2.waitForTransactionReceipt({
  hash: await l2Wallet.writeContract({
    address: l2Native,
    abi: erc20Abi,
    functionName: 'approve',
    args: [L2_STANDARD_BRIDGE, parseEther('25')],
  }),
})
const l1EthBefore = await l1.getBalance({ address: user.address })
const withdrawn = (await l2.getBalance({ address: user.address })) - eth(0.5)
const ethHash = await initiate('ETH', withdrawETH(l2Wallet, user.address, withdrawn))
const tokenHash = await initiate('RWA', withdrawERC20(l2Wallet, l2Token, l1Token, user.address, parseEther('40')))
const nativeHash = await initiate('L2-native RWA', withdrawERC20(l2Wallet, l2Native, l1Copy, user.address, parseEther('25')))
const [ethPaid, tokensPaid, nativePaid] = await Promise.all([
  waitForPayout('ETH', ethHash),
  waitForPayout('RWA', tokenHash),
  waitForPayout('L2-native RWA', nativeHash),
])
log('demo', ethPaid ? `withdrew ${formatEther(withdrawn)} ETH` : `${formatEther(withdrawn)} ETH stays in the Safe`, {
  l1Received: formatEther((await l1.getBalance({ address: user.address })) - l1EthBefore),
})
log('demo', tokensPaid ? 'withdrew 40 RWA' : '40 RWA stay in the Safe', {
  l1Balance: formatEther(await tokenBalance(l1, l1Token, user.address)),
  safeHolds: formatEther(await tokenBalance(l1, l1Token, c.safe)),
})

// 5. The L1 copies of the L2-native token go back to L2 through the Safe.
if (nativePaid) {
  log('demo', 'withdrew 25 L2-native RWA', { l1CopyBalance: formatEther(await tokenBalance(l1, l1Copy, user.address)) })
  const l2NativeBefore = await tokenBalance(l2, l2Native, user.address)
  await l1.waitForTransactionReceipt({
    hash: await l1Wallet.writeContract({
      address: l1Copy,
      abi: erc20Abi,
      functionName: 'approve',
      args: [c.gateway, parseEther('10')],
    }),
  })
  await l1.waitForTransactionReceipt({
    hash: await depositERC20(l1Wallet, c, l1Copy, l2Native, user.address, parseEther('10')),
  })
  await until('L2-native tokens back on L2', async () =>
    (await tokenBalance(l2, l2Native, user.address)) === l2NativeBefore + parseEther('10') ? true : undefined,
  )
  log('demo', 'deposited 10 back', {
    l1CopyBalance: formatEther(await tokenBalance(l1, l1Copy, user.address)),
    l2Balance: formatEther(await tokenBalance(l2, l2Native, user.address)),
  })
} else {
  log('demo', 'no L1 copies to deposit back')
}

// 6. The relayer key is compromised and queues a payout to the attacker for a withdrawal that does
//    not exist on L2. The watcher vetoes it within the delay. Off on public instances, where the veto
//    would show up as a withdrawal.
if (config.demoAttack) {
  const safeBefore = await l1.getBalance({ address: c.safe })
  const fakeHash = keccak256(toHex(`not a real withdrawal ${Date.now()}`))
  await l1.waitForTransactionReceipt({
    hash: await relayerKey.writeContract({
      address: c.module,
      abi: moduleAbi,
      functionName: 'queue',
      args: [fakeHash, attacker, eth(1), '0x'],
    }),
  })
  log('demo', `compromised relayer queued ${formatEther(eth(1))} ETH to the attacker`, { hash: fakeHash })
  await until(
    'veto',
    async () => {
      const [, queuedAt] = await l1.readContract({
        address: c.module,
        abi: moduleAbi,
        functionName: 'items',
        args: [fakeHash],
      })
      return queuedAt === 0n ? true : undefined
    },
    Number(delay) + 300,
  )
  await sleep(Number(delay) * 1000)
  try {
    await relayerKey.writeContract({
      address: c.module,
      abi: moduleAbi,
      functionName: 'execute',
      args: [fakeHash, attacker, eth(1), '0x'],
    })
    throw new Error('fake payout executed')
  } catch (e) {
    if ((e as Error).message === 'fake payout executed') throw e
    log('demo', 'attacker payout cannot execute after the delay: vetoed')
  }
  log('demo', 'Safe ETH unchanged by the attack', {
    before: formatEther(safeBefore),
    after: formatEther(await l1.getBalance({ address: c.safe })),
    attacker: formatEther(await l1.getBalance({ address: attacker })),
  })
}
log('demo', 'done')
