// Configuration from the env file in $SR_ENV (default: the devnet), plus the deployment init.sh wrote.
import { readFileSync } from 'node:fs'
import { type Hex, defineChain, fallback, http } from 'viem'
import type { Contracts } from './lib.ts'

const root = new URL('../', import.meta.url).pathname

const env = Object.fromEntries(
  readFileSync(process.env.SR_ENV ?? `${root}devnet/devnet.env`, 'utf8')
    .split('\n')
    .filter((line) => /^[A-Z0-9_]+=/.test(line))
    .map((line) => {
      const i = line.indexOf('=')
      return [line.slice(0, i), line.slice(i + 1).replace(/^"(.*)"$/, '$1')]
    }),
) as Record<string, string>
const data = env.DATA ?? `${root}.devnet`
export const dataDir = data

const deployment = JSON.parse(readFileSync(`${data}/addresses.json`, 'utf8'))
export const contracts: Contracts = { ...deployment, deployBlock: BigInt(deployment.deployBlock) }
export const keys = env as Record<string, Hex>
export const config = {
  vetoMarginSeconds: BigInt(env.VETO_MARGIN_SECONDS),
  pollMs: Number(env.POLL_MS),
  demoEth: env.DEMO_ETH,
  demoAttack: env.DEMO_ATTACK === 'true',
  explorerUrl: env.EXPLORER_URL as string | undefined,
}

export const rpc = {
  l1: env.L1_RPC,
  l1Fallbacks: env.L1_RPC_FALLBACKS?.split(',').filter(Boolean) ?? [],
  l2: 'http://127.0.0.1:9545',
  rollup: 'http://127.0.0.1:7545',
  l2Verifier: 'http://127.0.0.1:9645',
  rollupVerifier: 'http://127.0.0.1:7645',
}

/** L1 reads move on to the next RPC when one fails, e.g. on a rate limit or logs it has pruned. */
export const l1Transport = fallback([rpc.l1, ...rpc.l1Fallbacks].map((url) => http(url)))

export const rollup = JSON.parse(readFileSync(`${data}/rollup.json`, 'utf8'))
export const l1Chain = defineChain({
  id: rollup.l1_chain_id,
  name: 'L1',
  nativeCurrency: { name: 'Ether', symbol: 'ETH', decimals: 18 },
  rpcUrls: { default: { http: [rpc.l1] } },
})
export const l2Chain = defineChain({
  id: rollup.l2_chain_id,
  name: 'Safe rollup',
  nativeCurrency: { name: 'Ether', symbol: 'ETH', decimals: 18 },
  rpcUrls: { default: { http: [rpc.l2] } },
})

export async function safeL2Block(): Promise<bigint> {
  const res = await fetch(rpc.rollup, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'optimism_syncStatus', params: [] }),
  })
  const { result } = (await res.json()) as { result: { safe_l2: { number: number } } }
  return BigInt(result.safe_l2.number)
}

export const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

export function log(who: string, msg: string, extra: Record<string, unknown> = {}) {
  const fields = Object.entries(extra)
    .map(([k, v]) => `${k}=${v}`)
    .join(' ')
  console.log(`${new Date().toISOString().slice(11, 19)} [${who}] ${msg} ${fields}`)
}

export type { Address } from 'viem'
