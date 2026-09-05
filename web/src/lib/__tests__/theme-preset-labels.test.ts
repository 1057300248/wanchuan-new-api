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
import { createInstance } from 'i18next'
import { expect, test } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import vi from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'

import { THEME_PRESETS } from '../theme-customization'

const resources = { en, fr, ja, ru, vi, zh, 'zh-TW': zhTW }

test.each(Object.keys(resources))(
  'displays every theme preset label in %s without exposing a translation key',
  async (language) => {
    const i18n = createInstance()
    await i18n.init({ lng: language, fallbackLng: false, resources })

    for (const preset of THEME_PRESETS) {
      const key = `preset.${preset.value}`
      expect(i18n.exists(key), `${language}: ${key}`).toBe(true)
      expect(i18n.t(key)).not.toBe(key)
      expect(i18n.t(key).trim()).not.toBe('')
    }
  }
)
