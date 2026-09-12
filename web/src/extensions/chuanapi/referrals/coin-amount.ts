// JSON and the ledger use integer microcoins; inputs and labels use Coins.
// Parse text as integers to avoid parseInt truncation and floating-point drift.
export function parseCoinAmount(value: string, scale: number): number | null {
  if (!Number.isSafeInteger(scale) || scale <= 0) return null
  const places = Math.log10(scale)
  if (!Number.isInteger(places)) return null
  const match = /^(\d+)(?:\.(\d*))?$/.exec(value.trim())
  if (!match || (match[2]?.length ?? 0) > places || value.length > 40) {
    return null
  }
  const units =
    BigInt(match[1]) * BigInt(scale) +
    BigInt((match[2] ?? '').padEnd(places, '0') || '0')
  if (units <= 0n || units > BigInt(Number.MAX_SAFE_INTEGER)) return null
  return Number(units)
}

export function formatCoinAmount(units: number, scale: number): string {
  if (
    !Number.isSafeInteger(units) ||
    units < 0 ||
    !Number.isSafeInteger(scale) ||
    scale <= 0
  ) {
    return '-'
  }
  const places = Math.log10(scale)
  if (!Number.isInteger(places)) return '-'
  const value = BigInt(units)
  const divisor = BigInt(scale)
  if (places === 0) return String(value)
  return `${value / divisor}.${String(value % divisor).padStart(places, '0')}`
}
