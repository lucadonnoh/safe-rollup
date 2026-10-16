// End to end run on the devnet: deposits and withdrawals of ETH and an ERC20 through the Safe, then
// a compromised relayer key trying to drain the Safe, which the watcher vetoes.
import { readFileSync } from 'node:fs'
import {
  type Address,
  type Hash,
  type Hex,
  createPublicClient,
  createWalletClient,
  formatEther,
  http,
  keccak256,
  parseAbi,
  parseEther,
  toHex,
} from 'viem'
import { privateKeyToAccount } from 'viem/accounts'
import { contracts as c, keys, l1Chain, l2Chain, log, rpc, sleep } from './env.ts'
import {
  L2_STANDARD_BRIDGE,
  depositERC20,
  depositETH,
  erc20Abi,
  getWithdrawalStatus,
  getWithdrawals,
  moduleAbi,
  withdrawERC20,
  withdrawETH,
} from './lib.ts'

const factoryAbi = parseAbi([
  'function createOptimismMintableERC20(address remoteToken, string name, string symbol) returns (address)',
  'event OptimismMintableERC20Created(address indexed localToken, address indexed remoteToken, address deployer)',
])

const user = privateKeyToAccount(keys.USER_KEY)
const attacker: Address = '0x000000000000000000000000000000000000bad1'
const l1 = createPublicClient({ chain: l1Chain, transport: http(rpc.l1) })
const l2 = createPublicClient({ chain: l2Chain, transport: http(rpc.l2) })
const l1Wallet = createWalletClient({ account: user, chain: l1Chain, transport: http() })
const l2Wallet = createWalletClient({ account: user, chain: l2Chain, transport: http() })
const relayerKey = createWalletClient({
  account: privateKeyToAccount(keys.RELAYER_KEY),
  chain: l1Chain,
  transport: http(),
})

async function until<T>(what: string, fn: () => Promise<T | undefined>, timeoutSec = 180): Promise<T> {
  for (let i = 0; i < timeoutSec; i++) {
    const v = await fn()
    if (v !== undefined) return v
    await sleep(1000)
  }
  throw new Error(`timed out waiting for ${what}`)
}

const tokenBalance = (client: typeof l1 | typeof l2, token: Address, owner: Address) =>
  client.readContract({ address: token, abi: erc20Abi, functionName: 'balanceOf', args: [owner] })

async function waitForPayout(txHash: Hash) {
  const receipt = await l2.waitForTransactionReceipt({ hash: txHash })
  const [w] = getWithdrawals(receipt.logs)
  log('demo', 'withdrawal initiated on L2', { hash: w.withdrawalHash })
  let last = ''
  await until('payout', async () => {
    const s = await getWithdrawalStatus(l1, c, w.withdrawalHash)
    if (s.status !== last) log('demo', `status: ${s.status}`), (last = s.status)
    return s.status === 'executed' ? true : undefined
  })
}

function loadArtifact(name: string) {
  const path = `../../packages/contracts-bedrock/forge-artifacts/${name}.sol/${name}.json`
  const artifact = JSON.parse(readFileSync(new URL(path, import.meta.url), 'utf8'))
  return { abi: artifact.abi, bytecode: artifact.bytecode.object as Hex }
}

// 1. ETH deposit.
const ethBefore = await l2.getBalance({ address: user.address })
await l1.waitForTransactionReceipt({ hash: await depositETH(l1Wallet, c, user.address, parseEther('2')) })
await until('ETH on L2', async () => {
  const b = await l2.getBalance({ address: user.address })
  return b >= ethBefore + parseEther('2') ? b : undefined
})
log('demo', 'deposited 2 ETH', { l2Balance: formatEther(await l2.getBalance({ address: user.address })) })

// 2. ERC20 deposit, after creating its L2 representation with the standard predeploy factory.
const deployTx = await l1Wallet.deployContract({ ...loadArtifact('TestToken'), args: [user.address] })
const l1Token = (await l1.waitForTransactionReceipt({ hash: deployTx })).contractAddress!
const createTx = await l2Wallet.writeContract({
  address: '0x4200000000000000000000000000000000000012',
  abi: factoryAbi,
  functionName: 'createOptimismMintableERC20',
  args: [l1Token, 'Test Token', 'TEST'],
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
log('demo', 'deposited 100 TEST', { safeHolds: formatEther(await tokenBalance(l1, l1Token, c.safe)) })

// 3. ETH withdrawal.
const l1EthBefore = await l1.getBalance({ address: user.address })
await waitForPayout(await withdrawETH(l2Wallet, user.address, parseEther('0.5')))
const l1EthAfter = await l1.getBalance({ address: user.address })
log('demo', 'withdrew 0.5 ETH', { l1Received: formatEther(l1EthAfter - l1EthBefore) })

// 4. ERC20 withdrawal.
await waitForPayout(await withdrawERC20(l2Wallet, l2Token, l1Token, user.address, parseEther('40')))
log('demo', 'withdrew 40 TEST', {
  l1Balance: formatEther(await tokenBalance(l1, l1Token, user.address)),
  safeHolds: formatEther(await tokenBalance(l1, l1Token, c.safe)),
})

// 5. L2-native token: withdrawn to an L1 copy that the gateway mints, then deposited back.
const nativeTx = await l2Wallet.deployContract({ ...loadArtifact('TestToken'), args: [user.address] })
const l2Native = (await l2.waitForTransactionReceipt({ hash: nativeTx })).contractAddress!
const copyTx = await l1Wallet.deployContract({
  ...loadArtifact('OptimismMintableERC20'),
  args: [c.gateway, l2Native, 'Test Token on L1', 'TEST', 18],
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
await waitForPayout(await withdrawERC20(l2Wallet, l2Native, l1Copy, user.address, parseEther('25')))
log('demo', 'withdrew 25 L2-native TEST', { l1CopyBalance: formatEther(await tokenBalance(l1, l1Copy, user.address)) })
const l2NativeBefore = await tokenBalance(l2, l2Native, user.address)
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

// 6. The relayer key is compromised and queues a payout of 1 ETH to the attacker for a withdrawal
//    that does not exist on L2. The watcher vetoes it within the delay.
const safeBefore = await l1.getBalance({ address: c.safe })
const fakeHash = keccak256(toHex('not a real withdrawal'))
await l1.waitForTransactionReceipt({
  hash: await relayerKey.writeContract({
    address: c.module,
    abi: moduleAbi,
    functionName: 'queue',
    args: [fakeHash, attacker, parseEther('1'), '0x'],
  }),
})
log('demo', 'compromised relayer queued 1 ETH to the attacker', { hash: fakeHash })
await until('veto', async () => {
  const [, queuedAt] = await l1.readContract({ address: c.module, abi: moduleAbi, functionName: 'items', args: [fakeHash] })
  return queuedAt === 0n ? true : undefined
})
const delay = await l1.readContract({ address: c.module, abi: moduleAbi, functionName: 'delay' })
await sleep(Number(delay) * 1000)
try {
  await relayerKey.writeContract({
    address: c.module,
    abi: moduleAbi,
    functionName: 'execute',
    args: [fakeHash, attacker, parseEther('1'), '0x'],
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
log('demo', 'done')
