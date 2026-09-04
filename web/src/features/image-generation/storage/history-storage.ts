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
import { IMAGE_HISTORY_STORAGE_KEY } from '../constants'
import { isValidHistoryEntry, pruneHistoryEntries } from '../lib/history-utils'
import { isBlobSchemeUrl } from '../lib/managed-object-urls'
import type { ImageHistoryEntry } from '../types'

export function loadHistoryEntries(): ImageHistoryEntry[] {
  try {
    const raw = window.localStorage.getItem(IMAGE_HISTORY_STORAGE_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    const valid = parsed.filter(isValidHistoryEntry).map((entry) => ({
      ...entry,
      results: entry.results.filter(
        (result) => !isBlobSchemeUrl(result.source)
      ),
    }))
    return pruneHistoryEntries(valid)
  } catch {
    return []
  }
}

export function saveHistoryEntries(entries: ImageHistoryEntry[]): void {
  try {
    const persistedEntries = entries.map((entry) => ({
      ...entry,
      results: entry.results.filter((result) => {
        if (result.type === 'b64' && !result.keptLocally) return false
        if (
          typeof result.source === 'string' &&
          isBlobSchemeUrl(result.source)
        ) {
          return false
        }
        return true
      }),
    }))
    const pruned = pruneHistoryEntries(persistedEntries)
    window.localStorage.setItem(
      IMAGE_HISTORY_STORAGE_KEY,
      JSON.stringify(pruned)
    )
  } catch {
    /* storage full or unavailable: keep in-memory state only */
  }
}

export function clearHistoryStorage(): void {
  try {
    window.localStorage.removeItem(IMAGE_HISTORY_STORAGE_KEY)
  } catch {
    /* ignore */
  }
}
