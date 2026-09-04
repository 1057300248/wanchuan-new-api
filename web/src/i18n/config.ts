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

import { extensionI18nOverrides } from '@/extensions'

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
 * Extension 自有文案经 generic bridge 注入，在此浅合并。
 * 上游 locales/*.json 保持零改动，升级时不产生冲突。
 */
function withOverride(base: Bundle, override?: Bundle): Bundle {
  if (!override) return base
  return {
    ...base,
    translation: { ...base.translation, ...override.translation },
  }
}

const extensionOverrides = extensionI18nOverrides()

export const resources = {
  en: withOverride(en, extensionOverrides.en),
  zhCN: withOverride(zhCN, extensionOverrides.zhCN),
  fr: withOverride(fr, extensionOverrides.fr),
  ru: withOverride(ru, extensionOverrides.ru),
  ja: withOverride(ja, extensionOverrides.ja),
  vi: withOverride(vi, extensionOverrides.vi),
  zhTW: withOverride(zhTW, extensionOverrides.zhTW),
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
