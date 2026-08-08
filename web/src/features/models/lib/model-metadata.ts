/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
// ============================================================================
// Model Catalog Metadata Helpers
//
// The models API stores array metadata (input_modalities, output_modalities,
// capabilities) as JSON array strings, and bonus_eligible as a nullable
// tri-state (true/false/null). These helpers convert between API values and
// form values without guessing or inventing defaults.
// ============================================================================

export type BonusEligibleState = 'unset' | 'eligible' | 'ineligible'

function normalizeMetadataItems(items: unknown[]): string[] {
  const seen = new Set<string>()
  const normalized: string[] = []
  for (const item of items) {
    if (typeof item !== 'string') continue
    const value = item.trim()
    if (!value || seen.has(value)) continue
    seen.add(value)
    normalized.push(value)
  }
  return normalized
}

/**
 * Parse a JSON array string into a string array. Empty, invalid or
 * non-array values degrade safely to an empty array.
 */
export function parseMetadataArray(
  raw: string | null | undefined
): string[] {
  if (!raw) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return normalizeMetadataItems(parsed)
  } catch {
    return []
  }
}

/**
 * Serialize a string array into a JSON array string, or '' when empty.
 */
export function serializeMetadataArray(items: string[]): string {
  const cleaned = normalizeMetadataItems(items)
  return cleaned.length > 0 ? JSON.stringify(cleaned) : ''
}

/**
 * Parse a positive-integer form value into a number, or null when empty.
 * Returns null for zero, negatives and non-numeric input.
 */
export function parsePositiveInt(value: string): number | null {
  const trimmed = value.trim()
  if (trimmed === '') return null
  if (!/^[1-9]\d*$/.test(trimmed)) return null
  const parsed = Number(trimmed)
  return Number.isSafeInteger(parsed) ? parsed : null
}

/**
 * Validate a positive-integer form value (empty is allowed).
 */
export function isValidPositiveInt(value: string): boolean {
  return parsePositiveInt(value) !== null || value.trim() === ''
}

/**
 * Map the API tri-state value to the form state. null (missing) means "not
 * configured" and must not be collapsed into false.
 */
export function parseBonusEligible(
  value: boolean | null | undefined
): BonusEligibleState {
  if (value === true) return 'eligible'
  if (value === false) return 'ineligible'
  return 'unset'
}

/**
 * Map the form state back to the API tri-state value.
 */
export function serializeBonusEligible(
  state: BonusEligibleState
): boolean | null {
  if (state === 'eligible') return true
  if (state === 'ineligible') return false
  return null
}
