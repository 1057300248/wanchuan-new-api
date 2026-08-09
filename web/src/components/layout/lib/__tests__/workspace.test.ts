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

import {
  ADMIN_WORKSPACE_HOME,
  CUSTOMER_WORKSPACE_HOME,
  isAdminWorkspaceSwitchVisible,
  normalizeWorkspacePath,
  resolveWorkspace,
} from '../workspace'

describe('resolveWorkspace', () => {
  test('customer routes resolve to the customer workspace', () => {
    for (const path of [
      '/dashboard/overview',
      '/dashboard',
      '/dashboard/',
      '/keys',
      '/usage-logs',
      '/usage-logs/common',
      '/usage-logs/task',
      '/wallet',
      '/wallet#referral',
      '/profile',
      '/playground',
      '/chat/0',
      '/chat2link',
      '/errors/not-found',
    ]) {
      assert.equal(resolveWorkspace(path), 'customer', path)
    }
  })

  test('admin routes resolve to the admin workspace', () => {
    for (const path of [
      '/channels',
      '/channels/1',
      '/models',
      '/models/metadata',
      '/models/deployments',
      '/users',
      '/redemption-codes',
      '/subscriptions',
      '/system-info',
      '/system-settings',
      '/system-settings/site',
      '/system-settings/billing',
      '/dashboard/models',
      '/dashboard/users',
      '/dashboard/flow',
    ]) {
      assert.equal(resolveWorkspace(path), 'admin', path)
    }
  })

  test('admin prefixes win over the broader customer /dashboard prefix', () => {
    assert.equal(resolveWorkspace('/dashboard/models'), 'admin')
    assert.equal(resolveWorkspace('/dashboard/overview'), 'customer')
  })

  test('prefix matching respects path-segment boundaries', () => {
    assert.equal(resolveWorkspace('/models-extra'), 'admin')
    assert.equal(resolveWorkspace('/dashboard/models-extra'), 'customer')
    assert.equal(resolveWorkspace('/walletx'), 'admin')
    assert.equal(resolveWorkspace('/usage-logs-extra'), 'admin')
  })

  test('unknown authenticated paths fall back to the admin shell', () => {
    assert.equal(resolveWorkspace('/some-future-page'), 'admin')
  })

  test('trailing slashes, query strings and hashes do not change the workspace', () => {
    assert.equal(resolveWorkspace('/wallet/'), 'customer')
    assert.equal(resolveWorkspace('/keys?page=2'), 'customer')
    assert.equal(resolveWorkspace('/wallet?show_history=true#referral'), 'customer')
    assert.equal(resolveWorkspace('/users/?page=1'), 'admin')
  })
})

describe('normalizeWorkspacePath', () => {
  test('strips query, hash and trailing slashes but keeps the root', () => {
    assert.equal(normalizeWorkspacePath('/wallet/?tab=1#x'), '/wallet')
    assert.equal(normalizeWorkspacePath('/'), '/')
    assert.equal(normalizeWorkspacePath('/dashboard/models'), '/dashboard/models')
  })
})

describe('isAdminWorkspaceSwitchVisible', () => {
  test('admin and super admin roles may enter the admin workspace', () => {
    assert.equal(isAdminWorkspaceSwitchVisible(ROLE.ADMIN), true)
    assert.equal(isAdminWorkspaceSwitchVisible(ROLE.SUPER_ADMIN), true)
  })

  test('regular users and unknown roles never see the admin switch', () => {
    assert.equal(isAdminWorkspaceSwitchVisible(ROLE.USER), false)
    assert.equal(isAdminWorkspaceSwitchVisible(ROLE.GUEST), false)
    assert.equal(isAdminWorkspaceSwitchVisible(undefined), false)
  })
})

describe('workspace home constants', () => {
  test('customer home is the post-login default and admin home is a management route', () => {
    assert.equal(CUSTOMER_WORKSPACE_HOME, '/dashboard/overview')
    assert.equal(ADMIN_WORKSPACE_HOME, '/dashboard/models')
    assert.equal(resolveWorkspace(CUSTOMER_WORKSPACE_HOME), 'customer')
    assert.equal(resolveWorkspace(ADMIN_WORKSPACE_HOME), 'admin')
  })
})
