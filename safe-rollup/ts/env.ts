// Devnet configuration: RPCs, deployed addresses and anvil test keys.
import { readFileSync } from 'node:fs'
import { type Address, type Hex, defineChain } from 'viem'
import type { Contracts } from './lib.ts'

const root = new URL('../', import.meta.url).pathname

const keys = Object.fromEntries(
  readFileSync(`${root}devnet/accounts.env`, 'utf8')
    .split('\n')
    .filter((line) => line.includes('='))
    .map((line) => line.split('=') as [string, string]),
) as Record<string, Hex>

export const contracts: Contracts = JSON.parse(readFileSync(`${root}.devnet/addresses.json`, 'utf8'))
export { keys }

export const rpc = {
  l1: 'http://127.0.0.1:8545',
  l2: 'http://127.0.0.1:9545',
  rollup: 'http://127.0.0.1:7545',
  l2Verifier: 'http://127.0.0.1:9645',
}

export const l1Chain = defineChain({
  id: 900,
  name: 'L1',
  nativeCurrency: { name: 'Ether', symbol: 'ETH', decimals: 18 },
  rpcUrls: { default: { http: [rpc.l1] } },
})
export const l2Chain = defineChain({
  id: 901,
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

export type { Address }
