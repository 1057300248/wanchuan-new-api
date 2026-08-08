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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  getOverviewPanelVisibility,
  type OverviewPanelFlags,
} from '../overview-panels'

const ALL_ON: OverviewPanelFlags = {
  apiInfo: true,
  announcements: true,
  faq: true,
  uptimeKuma: true,
}

describe('getOverviewPanelVisibility', () => {
  test('admin keeps every configured panel', () => {
    assert.deepEqual(getOverviewPanelVisibility(ALL_ON, true), ALL_ON)
  })

  test('admin keeps the exact mixed configuration', () => {
    const mixed: OverviewPanelFlags = {
      apiInfo: false,
      announcements: true,
      faq: true,
      uptimeKuma: false,
    }
    assert.deepEqual(getOverviewPanelVisibility(mixed, true), mixed)
  })

  test('regular user only keeps announcements and drops monitoring/FAQ/API info panels', () => {
    assert.deepEqual(getOverviewPanelVisibility(ALL_ON, false), {
      apiInfo: false,
      announcements: true,
      faq: false,
      uptimeKuma: false,
    })
  })

  test('regular user keeps announcements disabled when the config disables them', () => {
    const withoutAnnouncements: OverviewPanelFlags = {
      apiInfo: true,
      announcements: false,
      faq: true,
      uptimeKuma: true,
    }
    assert.deepEqual(getOverviewPanelVisibility(withoutAnnouncements, false), {
      apiInfo: false,
      announcements: false,
      faq: false,
      uptimeKuma: false,
    })
  })
})
