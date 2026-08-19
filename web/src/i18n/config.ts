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
import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'

import { convertDetectedLanguage } from './languages'
import chuanapiEn from './locales/chuanapi/en.json'
import chuanapiFr from './locales/chuanapi/fr.json'
import chuanapiJa from './locales/chuanapi/ja.json'
import chuanapiRu from './locales/chuanapi/ru.json'
import chuanapiVi from './locales/chuanapi/vi.json'
import chuanapiZhTW from './locales/chuanapi/zh-TW.json'
import chuanapiZhCN from './locales/chuanapi/zh.json'
import en from './locales/en.json'
import fr from './locales/fr.json'
import ja from './locales/ja.json'
import ru from './locales/ru.json'
import vi from './locales/vi.json'
import zhTW from './locales/zh-TW.json'
import zhCN from './locales/zh.json'

type Bundle = { translation: Record<string, string> }

/**
 * ChuanAPI 自有文案独立成 locales/chuanapi/*.json，在此浅合并。
 * 上游 locales/*.json 保持零改动，升级时不产生冲突。
 */
function withOverride(base: Bundle, override: Bundle): Bundle {
  return {
    ...base,
    translation: { ...base.translation, ...override.translation },
  }
}

export const resources = {
  en: withOverride(en, chuanapiEn),
  zhCN: withOverride(zhCN, chuanapiZhCN),
  fr: withOverride(fr, chuanapiFr),
  ru: withOverride(ru, chuanapiRu),
  ja: withOverride(ja, chuanapiJa),
  vi: withOverride(vi, chuanapiVi),
  zhTW: withOverride(zhTW, chuanapiZhTW),
} as const

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    fallbackLng: 'en',
    supportedLngs: ['en', 'zhCN', 'fr', 'ru', 'ja', 'vi', 'zhTW'],
    load: 'currentOnly',
    nsSeparator: false, // Allow literal colons in keys (e.g., URLs, labels)
    debug: import.meta.env.DEV,
    interpolation: {
      escapeValue: false, // not needed for react as it escapes by default
    },
    detection: {
      order: ['localStorage', 'navigator'],
      caches: ['localStorage'],
      // Browsers report `zh-CN`/`zh-TW`/`zh`; map them onto our `zhCN`/`zhTW`
      // codes (non-Chinese codes pass through for normal supportedLngs matching).
      convertDetectedLanguage,
    },
  })

export default i18n
