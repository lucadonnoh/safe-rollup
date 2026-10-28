// Public face of a Safe rollup instance, on 127.0.0.1:8090 behind a tunnel: a status page built from
// chain data only, and a rate-limited JSON-RPC proxy to the sequencer for wallets.
import { existsSync, readFileSync, renameSync, writeFileSync } from 'node:fs'
import { type IncomingMessage, type ServerResponse, createServer } from 'node:http'
import {
  type Address,
  type Hash,
  type Hex,
  createPublicClient,
  decodeFunctionData,
  formatEther,
  formatUnits,
  getAddress,
  hexToBigInt,
  http,
  parseAbi,
  sliceHex,
} from 'viem'
import { contracts as c, config, dataDir, keys, l1Chain, l1Transport, l2Chain, log, rollup, rpc, sleep } from './env.ts'
import {
  L1_LAG,
  L2_CROSS_DOMAIN_MESSENGER,
  L2_TO_L1_MESSAGE_PASSER,
  erc20Abi,
  forcedTransfer,
  gatewayAbi,
  inRanges,
  messagePassedEvent,
  mintableAbi,
  moduleAbi,
  vetoReason,
} from './lib.ts'

const PORT = 8090
const MAX_BODY = 128 * 1024
const MAX_BATCH = 20
const MAX_LOG_RANGE = 10_000n
const RATE = { perSecond: 10, burst: 50 }
const RECENT = 25

const RPC_METHODS = new Set([
  'eth_blockNumber',
  'eth_call',
  'eth_chainId',
  'eth_estimateGas',
  'eth_feeHistory',
  'eth_gasPrice',
  'eth_getBalance',
  'eth_getBlockByHash',
  'eth_getBlockByNumber',
  'eth_getBlockReceipts',
  'eth_getCode',
  'eth_getLogs',
  'eth_getStorageAt',
  'eth_getTransactionByHash',
  'eth_getTransactionCount',
  'eth_getTransactionReceipt',
  'eth_maxPriorityFeePerGas',
  'eth_sendRawTransaction',
  'eth_syncing',
  'net_version',
  'web3_clientVersion',
])

const safeAbi = parseAbi(['function getOwners() view returns (address[])', 'function getThreshold() view returns (uint256)'])
const explorer = l1Chain.id === 11155111 ? 'https://sepolia.etherscan.io' : undefined
const safeApp = l1Chain.id === 11155111 ? `https://app.safe.global/home?safe=sep:${c.safe}` : undefined
const page = readFileSync(new URL('../site/index.html', import.meta.url), 'utf8')
const asset = (name: string) => readFileSync(new URL(`../site/${name}`, import.meta.url))
const icons: Record<string, [string, Buffer]> = {
  '/favicon.svg': ['image/svg+xml', asset('favicon.svg')],
  '/favicon.png': ['image/png', asset('favicon.png')],
  '/favicon.ico': ['image/png', asset('favicon.png')],
  '/apple-touch-icon.png': ['image/png', asset('apple-touch-icon.png')],
}

const l1 = createPublicClient({ chain: l1Chain, transport: l1Transport })
const l2 = createPublicClient({ chain: l2Chain, transport: http(rpc.l2Verifier) })

type Payout = {
  hash: Hash
  to: Address
  value: string
  queuedTx: Hash
  queuedAt: number
  executableAt: number
  status: 'queued' | 'executed' | 'vetoed'
  doneTx?: Hash
  doneAt?: number
  reason?: string
  onL2?: number
  token?: { symbol: string; amount: string }
  message?: boolean
}
type Deposit = { tx: Hash; block: number; from: Address; to: Address; mint: string; kind: 'ETH' | 'message' | 'forced' }
type Forced = { tx: Hash; at: number; from: Address; to: Address; reason?: string; token?: { symbol: string; amount: string }; recipient?: Address }

const payouts = new Map<Hash, Payout>()
const deposits: Deposit[] = []
let depositCount = 0
const forced: Forced[] = []
let forcedCount = 0
const l2Withdrawals = new Map<Hash, number>()
const blockTimes = new Map<bigint, number>()
let nextL1 = c.deployBlock
let nextL2 = 0n
let fixed: Record<string, unknown> = {}
// The last status survives restarts: rescanning L1 takes minutes, and the page would be empty meanwhile.
const SNAPSHOT_FILE = `${dataDir}/status-snapshot.json`
let snapshot = existsSync(SNAPSHOT_FILE) ? readFileSync(SNAPSHOT_FILE, 'utf8') : '{}'

async function blockTime(n: bigint) {
  if (!blockTimes.has(n)) blockTimes.set(n, Number((await l1.getBlock({ blockNumber: n })).timestamp))
  return blockTimes.get(n)!
}

const tokenAbi = parseAbi(['function symbol() view returns (string)', 'function decimals() view returns (uint8)'])
const tokens = new Map<string, { symbol: string; decimals: number } | null>()

async function tokenInfo(client: typeof l1 | typeof l2, token: Address) {
  const key = `${client === l1 ? 'l1' : 'l2'}:${token}`
  if (!tokens.has(key)) {
    try {
      const [symbol, decimals] = await Promise.all([
        client.readContract({ address: token, abi: tokenAbi, functionName: 'symbol' }),
        client.readContract({ address: token, abi: tokenAbi, functionName: 'decimals' }),
      ])
      tokens.set(key, { symbol: symbol.slice(0, 12), decimals })
    } catch {
      tokens.set(key, null)
    }
  }
  return tokens.get(key)
}

/** What a payout's Safe call moves besides its ETH value: an ERC20 amount or a relayed message. */
async function payoutAsset(to: Address, data: Hex): Promise<Pick<Payout, 'token' | 'message'>> {
  let call
  try {
    call = decodeFunctionData({ abi: [...erc20Abi, ...gatewayAbi, ...mintableAbi], data })
  } catch {
    return {}
  }
  if (call.functionName === 'relayMessage') return { message: true }
  if (call.functionName !== 'transfer' && call.functionName !== 'mint') return {}
  const info = await tokenInfo(l1, to)
  return info ? { token: { symbol: info.symbol, amount: formatUnits(call.args[1], info.decimals) } } : {}
}

/** The token amount and recipient of an L2 call, if it is an ERC20 transfer. */
async function l2Transfer(token: Address, data: Hex): Promise<Pick<Forced, 'token' | 'recipient'>> {
  try {
    const call = decodeFunctionData({ abi: erc20Abi, data })
    if (call.functionName !== 'transfer') return {}
    const [recipient, amount] = call.args
    const info = await tokenInfo(l2, token)
    return info ? { recipient, token: { symbol: info.symbol, amount: formatUnits(amount, info.decimals) } } : { recipient }
  } catch {
    return {}
  }
}

async function syncStatus(url: string) {
  const res = await fetch(url, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'optimism_syncStatus', params: [] }),
  })
  return ((await res.json()) as { result: Record<string, { number: number; hash: Hash; timestamp: number }> }).result
}

async function refresh() {
  const l2Head = await l2.getBlockNumber()
  if (l2Head >= nextL2) {
    const logs = await l2.getLogs({ address: L2_TO_L1_MESSAGE_PASSER, event: messagePassedEvent, fromBlock: nextL2, toBlock: l2Head })
    for (const e of logs) l2Withdrawals.set(e.args.withdrawalHash!, Number(e.blockNumber))
    nextL2 = l2Head + 1n
  }

  const l1Head = (await l1.getBlockNumber()) - L1_LAG
  if (l1Head >= nextL1) {
    const [moduleLogs, gatewayLogs] = await Promise.all([
      inRanges((fromBlock, toBlock) => l1.getContractEvents({ address: c.module, abi: moduleAbi, fromBlock, toBlock }), nextL1, l1Head),
      inRanges(
        (fromBlock, toBlock) =>
          l1.getContractEvents({ address: c.gateway, abi: gatewayAbi, eventName: 'TransactionDeposited', fromBlock, toBlock }),
        nextL1,
        l1Head,
      ),
    ])
    for (const e of moduleLogs) {
      const hash = e.args.withdrawalHash as Hash
      if (e.eventName === 'Queued') {
        const queuedAt = await blockTime(e.blockNumber)
        payouts.set(hash, {
          hash,
          to: e.args.to!,
          value: formatEther(e.args.value!),
          queuedTx: e.transactionHash,
          queuedAt,
          executableAt: queuedAt + Number(fixed.delay),
          status: 'queued',
          ...(await payoutAsset(e.args.to!, e.args.data!)),
        })
      } else {
        const p = payouts.get(hash)
        if (!p) continue
        Object.assign(p, {
          status: e.eventName === 'Executed' ? 'executed' : 'vetoed',
          doneTx: e.transactionHash,
          doneAt: await blockTime(e.blockNumber),
        })
        // A veto's reason is recorded in the Safe transaction that performs it.
        if (e.eventName === 'Vetoed') p.reason = vetoReason((await l1.getTransaction({ hash: e.transactionHash })).input)
      }
    }
    depositCount += gatewayLogs.length
    for (const e of gatewayLogs) {
      // A deposit the Safe made with depositTransactionOverride runs on L2 in another account's name.
      const tx = await l1.getTransaction({ hash: e.transactionHash })
      const f = tx.to && getAddress(tx.to) === getAddress(c.safe) ? forcedTransfer(tx.input) : undefined
      if (f) {
        forcedCount++
        forced.push({
          tx: e.transactionHash,
          at: await blockTime(e.blockNumber),
          from: f.from,
          to: f.to,
          reason: f.reason,
          ...(await l2Transfer(f.to, f.l2Call)),
        })
      }
      deposits.push({
        tx: e.transactionHash,
        block: Number(e.blockNumber),
        from: e.args.from!,
        to: e.args.to!,
        mint: formatEther(hexToBigInt(sliceHex(e.args.opaqueData!, 0, 32))),
        kind: f ? 'forced' : e.args.to === L2_CROSS_DOMAIN_MESSENGER ? 'message' : 'ETH',
      })
    }
    forced.splice(0, Math.max(0, forced.length - RECENT))
    deposits.splice(0, Math.max(0, deposits.length - RECENT))
    nextL1 = l1Head + 1n
  }
  for (const p of payouts.values()) p.onL2 = l2Withdrawals.get(p.hash)

  const [sequencer, verifier, safeWei, batcherWei, relayerWei] = await Promise.all([
    syncStatus(rpc.rollup),
    syncStatus(rpc.rollupVerifier),
    l1.getBalance({ address: c.safe }),
    l1.getBalance({ address: keys.BATCHER_ADDR }),
    l1.getBalance({ address: keys.RELAYER_ADDR }),
  ])
  const head = (b: { number: number; hash: Hash; timestamp: number }) => ({ number: b.number, hash: b.hash, timestamp: b.timestamp })
  const all = [...payouts.values()]
  snapshot = JSON.stringify({
    ...fixed,
    updatedAt: Math.floor(Date.now() / 1000),
    totals: {
      deposits: depositCount,
      forced: forcedCount,
      payouts: all.length,
      executed: all.filter((p) => p.status === 'executed').length,
      vetoed: all.filter((p) => p.status === 'vetoed').length,
    },
    balances: { safe: formatEther(safeWei), batcher: formatEther(batcherWei), relayer: formatEther(relayerWei) },
    sync: {
      unsafe: head(sequencer.unsafe_l2),
      safe: head(verifier.safe_l2),
      finalized: head(verifier.finalized_l2),
      l1Derived: head(verifier.current_l1),
      l1Head: head(verifier.head_l1),
    },
    payouts: [...payouts.values()].slice(-RECENT).reverse(),
    deposits: [...deposits].reverse(),
    forced: [...forced].reverse(),
  })
  writeFileSync(`${SNAPSHOT_FILE}.tmp`, snapshot)
  renameSync(`${SNAPSHOT_FILE}.tmp`, SNAPSHOT_FILE)
}

async function loadFixed() {
  const [owners, threshold, delay] = await Promise.all([
    l1.readContract({ address: c.safe, abi: safeAbi, functionName: 'getOwners' }),
    l1.readContract({ address: c.safe, abi: safeAbi, functionName: 'getThreshold' }),
    l1.readContract({ address: c.module, abi: moduleAbi, functionName: 'delay' }),
  ])
  fixed = {
    l1ChainId: l1Chain.id,
    l2ChainId: l2Chain.id,
    since: await blockTime(c.deployBlock),
    explorer,
    safeApp,
    l2Explorer: config.explorerUrl,
    contracts: { safe: c.safe, gateway: c.gateway, module: c.module, batchInbox: rollup.batch_inbox_address },
    hotKeys: { batcher: keys.BATCHER_ADDR, relayer: keys.RELAYER_ADDR },
    owners,
    threshold: Number(threshold),
    delay: Number(delay),
  }
}

// Per-visitor token buckets, keyed by the address Cloudflare reports.
const buckets = new Map<string, { tokens: number; at: number }>()
function allow(req: IncomingMessage, cost: number) {
  const ip = (req.headers['cf-connecting-ip'] as string | undefined) ?? req.socket.remoteAddress ?? ''
  const now = Date.now() / 1000
  const b = buckets.get(ip) ?? { tokens: RATE.burst, at: now }
  b.tokens = Math.min(RATE.burst, b.tokens + (now - b.at) * RATE.perSecond)
  b.at = now
  buckets.set(ip, b)
  if (b.tokens < cost) return false
  b.tokens -= cost
  return true
}
setInterval(() => {
  const stale = Date.now() / 1000 - 600
  for (const [ip, b] of buckets) if (b.at < stale) buckets.delete(ip)
}, 60_000)

type RpcCall = { id?: unknown; method?: unknown; params?: unknown }
function refusal(call: RpcCall): string | undefined {
  if (typeof call.method !== 'string' || !RPC_METHODS.has(call.method)) return 'method not allowed'
  if (call.method === 'eth_getLogs') {
    const [filter] = (Array.isArray(call.params) ? call.params : []) as { fromBlock?: string; toBlock?: string; blockHash?: string }[]
    if (filter?.blockHash) return
    const from = filter?.fromBlock?.startsWith('0x') ? BigInt(filter.fromBlock) : undefined
    const to = filter?.toBlock?.startsWith('0x') ? BigInt(filter.toBlock) : undefined
    if (from === undefined || to === undefined || to - from > MAX_LOG_RANGE) {
      return `eth_getLogs needs numeric fromBlock and toBlock at most ${MAX_LOG_RANGE} blocks apart`
    }
  }
}

function send(res: ServerResponse, status: number, type: string, body: string | Buffer, extra: Record<string, string> = {}) {
  res.writeHead(status, { 'content-type': type, 'access-control-allow-origin': '*', ...extra })
  res.end(body)
}

async function proxy(req: IncomingMessage, res: ServerResponse, body: string) {
  let parsed: RpcCall | RpcCall[]
  try {
    parsed = JSON.parse(body)
  } catch {
    return send(res, 400, 'application/json', JSON.stringify({ jsonrpc: '2.0', id: null, error: { code: -32700, message: 'parse error' } }))
  }
  const calls = Array.isArray(parsed) ? parsed : [parsed]
  if (calls.length === 0 || calls.length > MAX_BATCH) {
    return send(res, 400, 'application/json', JSON.stringify({ jsonrpc: '2.0', id: null, error: { code: -32600, message: `batches hold 1 to ${MAX_BATCH} calls` } }))
  }
  if (!allow(req, calls.length)) return send(res, 429, 'application/json', JSON.stringify({ jsonrpc: '2.0', id: null, error: { code: -32005, message: 'rate limited' } }))
  const refused = calls.map(refusal)
  const forwarded = calls.filter((_, i) => !refused[i])
  let answers: { id?: unknown }[] = []
  if (forwarded.length > 0) {
    const upstream = await fetch(rpc.l2, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(forwarded),
      signal: AbortSignal.timeout(15_000),
    })
    answers = (await upstream.json()) as { id?: unknown }[]
  }
  let next = 0
  const results = calls.map((call, i) =>
    refused[i] ? { jsonrpc: '2.0', id: call.id ?? null, error: { code: -32601, message: refused[i] } } : answers[next++],
  )
  send(res, 200, 'application/json', JSON.stringify(Array.isArray(parsed) ? results : results[0]))
}

const server = createServer((req, res) => {
  const path = (req.url ?? '/').split('?')[0]
  if (req.method === 'OPTIONS') {
    return send(res, 204, 'text/plain', '', { 'access-control-allow-methods': 'POST', 'access-control-allow-headers': 'content-type' })
  }
  if (req.method === 'GET' && path === '/') return send(res, 200, 'text/html; charset=utf-8', page, { 'cache-control': 'max-age=300' })
  if (req.method === 'GET' && icons[path]) return send(res, 200, icons[path][0], icons[path][1], { 'cache-control': 'max-age=86400' })
  // Blockscout cannot live under a sub-path, so /explorer forwards to its own hostname.
  if (req.method === 'GET' && config.explorerUrl && (path === '/explorer' || path.startsWith('/explorer/'))) {
    return send(res, 302, 'text/plain', '', { location: config.explorerUrl + (req.url ?? '').slice('/explorer'.length) })
  }
  if (req.method === 'GET' && path === '/api/status') {
    if (!allow(req, 1)) return send(res, 429, 'text/plain', 'rate limited')
    return send(res, 200, 'application/json', snapshot, { 'cache-control': 'no-store' })
  }
  if (req.method === 'POST' && (path === '/' || path === '/rpc')) {
    const chunks: Buffer[] = []
    let size = 0
    req.on('data', (chunk: Buffer) => {
      size += chunk.length
      if (size > MAX_BODY) {
        send(res, 413, 'text/plain', 'body too large')
        req.destroy()
      } else chunks.push(chunk)
    })
    req.on('end', () => {
      if (size > MAX_BODY) return
      proxy(req, res, Buffer.concat(chunks).toString('utf8')).catch((e) => {
        log('server', 'rpc proxy failed', { error: (e as Error).message })
        send(res, 502, 'application/json', JSON.stringify({ jsonrpc: '2.0', id: null, error: { code: -32603, message: 'sequencer unavailable' } }))
      })
    })
    return
  }
  send(res, 404, 'text/plain', 'not found')
})
server.requestTimeout = 30_000
server.headersTimeout = 10_000

await loadFixed()
server.listen(PORT, '127.0.0.1', () => log('server', `listening on 127.0.0.1:${PORT}`))
while (true) {
  try {
    await refresh()
  } catch (e) {
    log('server', 'refresh failed', { error: (e as Error).message })
  }
  await sleep(config.pollMs)
}
