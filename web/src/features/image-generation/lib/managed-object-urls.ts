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
export function getImageUrlScheme(url: string): string {
  const trimmed = url.trimStart()
  const colon = trimmed.indexOf(':')
  if (colon <= 0) return ''
  return trimmed.slice(0, colon).toLowerCase()
}

export function isBlobSchemeUrl(url: string): boolean {
  return getImageUrlScheme(url) === 'blob'
}

export function isDataSchemeUrl(url: string): boolean {
  return getImageUrlScheme(url) === 'data'
}

export function isHttpSchemeUrl(url: string): boolean {
  const scheme = getImageUrlScheme(url)
  return scheme === 'http' || scheme === 'https'
}

const managedObjectUrls = new Map<string, number>()

function normalizeManagedKey(url: string): string {
  const trimmed = url.trimStart()
  const colon = trimmed.indexOf(':')
  if (colon <= 0) return trimmed
  return `blob:${trimmed.slice(colon + 1)}`
}

export function createManagedObjectUrl(blob: Blob): string {
  const url = URL.createObjectURL(blob)
  const key = normalizeManagedKey(url)
  managedObjectUrls.set(key, (managedObjectUrls.get(key) ?? 0) + 1)
  return url
}

export function revokeManagedObjectUrl(url: string): void {
  try {
    URL.revokeObjectURL(url)
  } catch {
    /* ignore revoke failures and still drop the registration */
  }
  const key = normalizeManagedKey(url)
  const count = managedObjectUrls.get(key)
  if (count === undefined) return
  if (count <= 1) managedObjectUrls.delete(key)
  else managedObjectUrls.set(key, count - 1)
}

export function isManagedObjectUrl(url: string): boolean {
  if (!isBlobSchemeUrl(url)) return false
  return managedObjectUrls.has(normalizeManagedKey(url))
}

export function clearManagedObjectUrls(): void {
  managedObjectUrls.clear()
}
