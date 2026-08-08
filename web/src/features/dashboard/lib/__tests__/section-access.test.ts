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

import { ROLE } from '@/lib/roles'

import { DASHBOARD_DEFAULT_SECTION } from '../../section-registry'
import {
  DASHBOARD_ADMIN_ONLY_SECTIONS,
  DASHBOARD_OVERVIEW_SECTION,
  isDashboardSectionAdminOnly,
  isDashboardSectionAccessible,
  resolveDashboardSectionForRole,
} from '../section-access'

describe('isDashboardSectionAdminOnly', () => {
  test('marks the analytics sections models/flow/users as admin-only', () => {
    assert.deepEqual(DASHBOARD_ADMIN_ONLY_SECTIONS, ['models', 'flow', 'users'])
    assert.equal(isDashboardSectionAdminOnly('models'), true)
    assert.equal(isDashboardSectionAdminOnly('flow'), true)
    assert.equal(isDashboardSectionAdminOnly('users'), true)
  })

  test('overview is never admin-only', () => {
    assert.equal(isDashboardSectionAdminOnly(DASHBOARD_OVERVIEW_SECTION), false)
  })
})

describe('isDashboardSectionAccessible', () => {
  test('admin and super admin can open every analytics section', () => {
    for (const section of DASHBOARD_ADMIN_ONLY_SECTIONS) {
      assert.equal(isDashboardSectionAccessible(section, ROLE.ADMIN), true)
      assert.equal(
        isDashboardSectionAccessible(section, ROLE.SUPER_ADMIN),
        true
      )
    }
  })

  test('regular user cannot open analytics sections', () => {
    for (const section of DASHBOARD_ADMIN_ONLY_SECTIONS) {
      assert.equal(isDashboardSectionAccessible(section, ROLE.USER), false)
    }
  })

  test('guest and unknown role are treated as non-admin', () => {
    assert.equal(isDashboardSectionAccessible('models', ROLE.GUEST), false)
    assert.equal(isDashboardSectionAccessible('models', undefined), false)
  })

  test('overview is accessible for every role', () => {
    assert.equal(
      isDashboardSectionAccessible(DASHBOARD_OVERVIEW_SECTION, ROLE.USER),
      true
    )
    assert.equal(
      isDashboardSectionAccessible(DASHBOARD_OVERVIEW_SECTION, undefined),
      true
    )
  })
})

describe('resolveDashboardSectionForRole', () => {
  test('non-admin on /dashboard/models is redirected to the customer overview', () => {
    assert.equal(
      resolveDashboardSectionForRole('models', ROLE.USER),
      DASHBOARD_OVERVIEW_SECTION
    )
  })

  test('non-admin on /dashboard/flow or /dashboard/users is redirected to the customer overview', () => {
    assert.equal(
      resolveDashboardSectionForRole('flow', ROLE.USER),
      DASHBOARD_OVERVIEW_SECTION
    )
    assert.equal(
      resolveDashboardSectionForRole('users', ROLE.USER),
      DASHBOARD_OVERVIEW_SECTION
    )
  })

  test('admin keeps the requested analytics section', () => {
    assert.equal(resolveDashboardSectionForRole('models', ROLE.ADMIN), 'models')
    assert.equal(resolveDashboardSectionForRole('flow', ROLE.ADMIN), 'flow')
    assert.equal(
      resolveDashboardSectionForRole('users', ROLE.SUPER_ADMIN),
      'users'
    )
  })

  test('overview resolves to itself for every role', () => {
    assert.equal(
      resolveDashboardSectionForRole(DASHBOARD_OVERVIEW_SECTION, ROLE.USER),
      DASHBOARD_OVERVIEW_SECTION
    )
    assert.equal(
      resolveDashboardSectionForRole(DASHBOARD_OVERVIEW_SECTION, undefined),
      DASHBOARD_OVERVIEW_SECTION
    )
  })
})

describe('dashboard landing contract', () => {
  test('the default landing section after /dashboard is the customer overview', () => {
    assert.equal(DASHBOARD_DEFAULT_SECTION, DASHBOARD_OVERVIEW_SECTION)
  })
})
