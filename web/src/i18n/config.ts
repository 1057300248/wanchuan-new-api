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

import { chuanapiI18nOverrides } from '@/extensions/chuanapi/i18n'

import { convertDetectedLanguage } from './languages'
import en from './locales/en.json'
import fr from './locales/fr.json'
import ja from './locales/ja.json'
import ru from './locales/ru.json'
import vi from './locales/vi.json'
import zhTW from './locales/zh-TW.json'
import zhCN from './locales/zh.json'

type Bundle = { translation: Record<string, string> }

/**
 * ChuanAPI 自有文案由 extension module 提供，在此浅合并。
 * 上游 locales/*.json 保持零改动，升级时不产生冲突。
 */
function withOverride(base: Bundle, override: Bundle): Bundle {
  return {
    ...base,
    translation: { ...base.translation, ...override.translation },
  }
}

export const resources = {
  en: withOverride(en, chuanapiI18nOverrides.en),
  zhCN: withOverride(zhCN, chuanapiI18nOverrides.zhCN),
  fr: withOverride(fr, chuanapiI18nOverrides.fr),
  ru: withOverride(ru, chuanapiI18nOverrides.ru),
  ja: withOverride(ja, chuanapiI18nOverrides.ja),
  vi: withOverride(vi, chuanapiI18nOverrides.vi),
  zhTW: withOverride(zhTW, chuanapiI18nOverrides.zhTW),
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
