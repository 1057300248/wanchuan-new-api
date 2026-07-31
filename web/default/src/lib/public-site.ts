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
export const MARKETING_HOME_URL = 'https://chuanapi.com'
export const QUICKSTART_DOCS_URL =
  'https://chuanapi.com/docs/quickstart/'

export function resolvePublicDocsUrl(value: unknown): string {
  if (typeof value !== 'string') return QUICKSTART_DOCS_URL

  const normalized = value.trim()
  if (!normalized || normalized === `${MARKETING_HOME_URL}/#quickstart`) {
    return QUICKSTART_DOCS_URL
  }

  return normalized
}

export function isPublicRegistrationEnabled(
  status: Record<string, unknown> | null
): boolean {
  return status?.register_enabled === true
}
