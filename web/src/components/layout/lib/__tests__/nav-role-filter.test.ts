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

import type { NavGroup } from '../../types'
import { filterNavGroupsByRole, isAdminRole } from '../nav-role-filter'

function buildFixtureNavGroups(): NavGroup[] {
  return [
    {
      id: 'chat',
      title: 'Chat',
      items: [
        { title: 'Playground', url: '/playground' },
        { title: 'Chat', type: 'chat-presets' },
      ],
    },
    {
      id: 'general',
      title: 'General',
      items: [
        { title: 'Overview', url: '/dashboard/overview' },
        {
          title: 'Dashboard',
          url: '/dashboard/models',
          requiredRole: ROLE.ADMIN,
        },
        { title: 'API Keys', url: '/keys' },
      ],
    },
    {
      id: 'admin',
      title: 'Admin',
      items: [
        { title: 'Channels', url: '/channels' },
        {
          title: 'System Info',
          url: '/system-info',
          requiredRole: ROLE.SUPER_ADMIN,
        },
      ],
    },
  ]
}

function collectItemUrls(navGroups: NavGroup[]): string[] {
  return navGroups.flatMap((group) =>
    group.items.flatMap((item) =>
      'url' in item && item.url ? [String(item.url)] : []
    )
  )
}

describe('isAdminRole', () => {
  test('admin and super admin roles are admin', () => {
    assert.equal(isAdminRole(ROLE.ADMIN), true)
    assert.equal(isAdminRole(ROLE.SUPER_ADMIN), true)
  })

  test('user, guest and unknown roles are not admin', () => {
    assert.equal(isAdminRole(ROLE.USER), false)
    assert.equal(isAdminRole(ROLE.GUEST), false)
    assert.equal(isAdminRole(undefined), false)
  })
})

describe('filterNavGroupsByRole', () => {
  test('regular user gets a customer sidebar without the admin group or analytics entry', () => {
    const filtered = filterNavGroupsByRole(buildFixtureNavGroups(), ROLE.USER)

    assert.deepEqual(
      filtered.map((group) => group.id),
      ['chat', 'general']
    )
    assert.deepEqual(collectItemUrls(filtered), [
      '/playground',
      '/dashboard/overview',
      '/keys',
    ])
  })

  test('guest is treated like a regular user', () => {
    const filtered = filterNavGroupsByRole(buildFixtureNavGroups(), ROLE.GUEST)

    assert.deepEqual(
      filtered.map((group) => group.id),
      ['chat', 'general']
    )
    assert.deepEqual(collectItemUrls(filtered), [
      '/playground',
      '/dashboard/overview',
      '/keys',
    ])
  })

  test('unknown role falls back to the guest view', () => {
    const filtered = filterNavGroupsByRole(buildFixtureNavGroups(), undefined)

    assert.deepEqual(collectItemUrls(filtered), [
      '/playground',
      '/dashboard/overview',
      '/keys',
    ])
  })

  test('admin keeps the admin group, the analytics entry, and items at or below their role', () => {
    const filtered = filterNavGroupsByRole(buildFixtureNavGroups(), ROLE.ADMIN)

    assert.deepEqual(
      filtered.map((group) => group.id),
      ['chat', 'general', 'admin']
    )
    assert.deepEqual(collectItemUrls(filtered), [
      '/playground',
      '/dashboard/overview',
      '/dashboard/models',
      '/keys',
      '/channels',
    ])
  })

  test('super admin additionally sees super-admin-only items', () => {
    const filtered = filterNavGroupsByRole(
      buildFixtureNavGroups(),
      ROLE.SUPER_ADMIN
    )

    assert.deepEqual(collectItemUrls(filtered), [
      '/playground',
      '/dashboard/overview',
      '/dashboard/models',
      '/keys',
      '/channels',
      '/system-info',
    ])
  })

  test('groups left empty after role filtering are dropped', () => {
    const groups: NavGroup[] = [
      {
        id: 'super-only',
        title: 'Super Only',
        items: [
          {
            title: 'System Info',
            url: '/system-info',
            requiredRole: ROLE.SUPER_ADMIN,
          },
        ],
      },
      {
        id: 'general',
        title: 'General',
        items: [{ title: 'Overview', url: '/dashboard/overview' }],
      },
    ]

    assert.deepEqual(
      filterNavGroupsByRole(groups, ROLE.USER).map((group) => group.id),
      ['general']
    )
    assert.deepEqual(
      filterNavGroupsByRole(groups, ROLE.SUPER_ADMIN).map((group) => group.id),
      ['super-only', 'general']
    )
  })
})
