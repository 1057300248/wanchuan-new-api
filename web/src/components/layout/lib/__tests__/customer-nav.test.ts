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

import type { TFunction } from 'i18next'

import type { NavGroup } from '../../types'
import { buildCustomerNavGroups } from '../customer-nav'

/** Translator stub that returns the English key itself. */
function createStubT(): TFunction {
  return ((key: string) => key) as TFunction
}

function collectUrls(groups: NavGroup[]): string[] {
  return groups.flatMap((group) =>
    group.items.flatMap((item) => ('url' in item && item.url ? [item.url] : []))
  )
}

const ADMIN_URLS = [
  '/channels',
  '/models',
  '/users',
  '/redemption-codes',
  '/subscriptions',
  '/system-info',
  '/system-settings',
  '/dashboard/models',
  '/dashboard/users',
  '/dashboard/flow',
]

describe('buildCustomerNavGroups', () => {
  test('covers the customer features: overview, keys, usage logs, wallet, referral, profile, playground', () => {
    const groups = buildCustomerNavGroups(createStubT())
    const urls = collectUrls(groups)

    for (const expected of [
      '/dashboard/overview',
      '/keys',
      '/usage-logs/common',
      '/wallet',
      '/wallet#referral',
      '/profile',
      '/playground',
    ]) {
      assert.equal(urls.includes(expected), true, `missing ${expected}`)
    }
  })

  test('contains no admin management entries', () => {
    const urls = collectUrls(buildCustomerNavGroups(createStubT()))
    for (const adminUrl of ADMIN_URLS) {
      assert.equal(urls.includes(adminUrl), false, `found admin url ${adminUrl}`)
    }
  })

  test('referral item anchors the wallet referral section and stays active on the plain wallet page', () => {
    const groups = buildCustomerNavGroups(createStubT())
    const referral = groups
      .flatMap((group) => group.items)
      .find((item) => 'url' in item && item.url === '/wallet#referral')

    assert.ok(referral)
    assert.ok('activeUrls' in referral)
    assert.deepEqual(referral.activeUrls, ['/wallet', '/wallet#referral'])
  })

  test('usage logs item stays active across every log section', () => {
    const groups = buildCustomerNavGroups(createStubT())
    const usageLogs = groups
      .flatMap((group) => group.items)
      .find((item) => 'url' in item && item.url === '/usage-logs/common')

    assert.ok(usageLogs)
    assert.ok('activeUrls' in usageLogs)
    assert.deepEqual(usageLogs.activeUrls, [
      '/usage-logs/common',
      '/usage-logs/task',
      '/usage-logs/drawing',
    ])
  })
})
