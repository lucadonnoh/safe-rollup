// Compliance review of payouts that match their L2 withdrawal. About one in three is vetoed, for a reason
// that is true of that withdrawal: a veto on every withdrawal exists so that the operator can use it. Whether
// to veto depends only on the withdrawal hash, so a restarted watcher reaches the same decision. The reason
// skips the codes of the last few vetoes, so consecutive findings differ.
import type { Hash } from 'viem'
import type { Payout } from './lib.ts'

type Rule = { code: string; reason: string; applies: (payout: Payout, now: Date) => boolean }

const always = () => true
const movesValue = (p: Payout) => p.value > 0n || (p.erc20?.amount ?? 0n) > 0n

const RULES: Rule[] = [
  { code: 'AML-07', reason: 'Unusual activity: the account attempted a withdrawal.', applies: always },
  { code: 'SOF-03', reason: 'Source of funds: the assets originated on Ethereum.', applies: always },
  { code: 'KYC-01', reason: 'Know your customer: the recipient has not completed identity verification.', applies: always },
  { code: 'TR-02', reason: 'Travel Rule: the beneficiary institution could not be identified.', applies: always },
  { code: 'RISK-05', reason: 'High-risk counterparty: the funds come from a rollup.', applies: always },
  { code: 'PUR-01', reason: 'Purpose of payment: the withdrawal did not state one.', applies: always },
  { code: 'GEO-02', reason: "Jurisdiction: the recipient's country of residence is unknown.", applies: always },
  { code: 'MHP-01', reason: 'Minimum holding period: the assets have been held for less than a year.', applies: always },
  { code: 'POR-01', reason: 'Proof of reserves: the payout would reduce reserves.', applies: (p) => p.value > 0n || !!p.erc20 },
  { code: 'EDD-02', reason: 'Enhanced due diligence: the amount is greater than zero.', applies: movesValue },
  {
    code: 'OPS-09',
    reason: 'Business hours: the settlement desk is closed. It opens Monday to Friday, 9:00 to 17:00 Central European Time.',
    applies: (_, now) => !deskOpen(now),
  },
]

function deskOpen(now: Date): boolean {
  const parts = new Intl.DateTimeFormat('en-GB', { timeZone: 'Europe/Berlin', weekday: 'short', hour: 'numeric', hourCycle: 'h23' })
    .formatToParts(now)
  const weekday = parts.find((p) => p.type === 'weekday')?.value
  const hour = Number(parts.find((p) => p.type === 'hour')?.value)
  return weekday !== 'Sat' && weekday !== 'Sun' && hour >= 9 && hour < 17
}

const recent: string[] = []

/** The veto reason for a valid payout, or undefined when compliance releases it. */
export function review(withdrawalHash: Hash, payout: Payout, now = new Date()): string | undefined {
  const n = BigInt(withdrawalHash)
  if (n % 3n !== 0n) return undefined
  const applicable = RULES.filter((r) => r.applies(payout, now))
  const fresh = applicable.filter((r) => !recent.includes(r.code))
  const rules = fresh.length > 0 ? fresh : applicable
  const rule = rules[Number((n / 3n) % BigInt(rules.length))]
  recent.push(rule.code)
  if (recent.length > 5) recent.shift()
  return `${rule.code} ${rule.reason}`
}
