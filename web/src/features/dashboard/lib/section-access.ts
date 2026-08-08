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

/**
 * Dashboard analytics sections that are only meaningful for admins.
 *
 * Regular users land on `/dashboard/overview` (customer console); the
 * model/flow/user analytics views are admin-only and must be guarded at
 * both the route level and the section navigation level.
 */
export const DASHBOARD_ADMIN_ONLY_SECTIONS = [
  'models',
  'flow',
  'users',
] as const

export type DashboardAdminOnlySection =
  (typeof DASHBOARD_ADMIN_ONLY_SECTIONS)[number]

export const DASHBOARD_OVERVIEW_SECTION = 'overview'

export function isDashboardSectionAdminOnly(section: string): boolean {
  return (DASHBOARD_ADMIN_ONLY_SECTIONS as readonly string[]).includes(section)
}

export function isDashboardSectionAccessible(
  section: string,
  role?: number
): boolean {
  if (!isDashboardSectionAdminOnly(section)) return true
  return (role ?? ROLE.GUEST) >= ROLE.ADMIN
}

/**
 * Resolve the section a user may view, redirecting non-admins away from
 * analytics sections to the customer overview.
 */
export function resolveDashboardSectionForRole(
  section: string,
  role?: number
): string {
  if (isDashboardSectionAccessible(section, role)) return section
  return DASHBOARD_OVERVIEW_SECTION
}
