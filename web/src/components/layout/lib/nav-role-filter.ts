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
import { ROLE } from '@/lib/roles'

import type { NavGroup, NavItem } from '../types'

export function isAdminRole(role: number | undefined): boolean {
  return (role ?? ROLE.GUEST) >= ROLE.ADMIN
}

export function isNavItemVisibleForRole(
  item: NavItem,
  role: number | undefined
): boolean {
  if (item.requiredRole === undefined) return true
  return (role ?? ROLE.GUEST) >= item.requiredRole
}

/**
 * Narrow sidebar navigation groups by the signed-in user's role.
 *
 * - The whole `admin` group is hidden for non-admins (customer console).
 * - Items carrying `requiredRole` are hidden below their threshold
 *   (e.g. the dashboard analytics entry for regular users).
 * - Groups left empty after filtering are dropped.
 *
 * Route-level guards enforce the same access independently.
 */
export function filterNavGroupsByRole(
  navGroups: NavGroup[],
  role: number | undefined
): NavGroup[] {
  const isAdmin = isAdminRole(role)
  return navGroups
    .filter((group) => isAdmin || group.id !== 'admin')
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => isNavItemVisibleForRole(item, role)),
    }))
    .filter((group) => group.items.length > 0)
}
