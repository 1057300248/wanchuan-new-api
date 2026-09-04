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
/**
 * Generic i18n override contract.
 *
 * Core i18n configuration must never import a concrete extension's locale
 * bundles. Extensions register overrides here; `extensionI18nOverrides()`
 * merges them in registration order so a later extension wins a conflicting
 * key deterministically.
 */
export type ExtensionLocaleKey =
  | 'en'
  | 'zhCN'
  | 'fr'
  | 'ru'
  | 'ja'
  | 'vi'
  | 'zhTW'

export type ExtensionLocaleBundle = { translation: Record<string, string> }

export type I18nExtension = {
  id: string
  overrides: Partial<Record<ExtensionLocaleKey, ExtensionLocaleBundle>>
}

let i18nExtensions: readonly I18nExtension[] = []

export function setI18nExtensions(extensions: readonly I18nExtension[]) {
  if (i18nExtensions.length > 0) {
    throw new Error('frontend i18n extensions are already configured')
  }
  i18nExtensions = Object.freeze([...extensions])
}

export function extensionI18nOverrides(): Partial<
  Record<ExtensionLocaleKey, ExtensionLocaleBundle>
> {
  const merged: Partial<Record<ExtensionLocaleKey, ExtensionLocaleBundle>> = {}
  for (const extension of i18nExtensions) {
    for (const [locale, bundle] of Object.entries(extension.overrides)) {
      const key = locale as ExtensionLocaleKey
      if (!bundle) continue
      const existing = merged[key]?.translation ?? {}
      merged[key] = {
        translation: { ...existing, ...bundle.translation },
      }
    }
  }
  return merged
}
