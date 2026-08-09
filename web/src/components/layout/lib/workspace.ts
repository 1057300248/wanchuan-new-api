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
 * Dual-workspace shell model (TokenMix customer center / admin console).
 *
 * The pathname decides which authenticated shell renders: customer routes
 * (`/dashboard/overview`, `/dashboard/keys`, `/dashboard/logs`,
 * `/dashboard/wallet`, `/dashboard/settings`, `/dashboard/playground`, chat
 * links, error pages) get the customer center; every
 * admin management route keeps the full admin shell. Unknown paths fall
 * back to the admin shell so future management pages cannot silently
 * surface inside the customer center.
 */
export type Workspace = 'customer' | 'admin'

/** Landing page of the customer workspace (also the post-login default). */
export const CUSTOMER_WORKSPACE_HOME = '/dashboard/overview'

/** Landing page of the admin workspace. */
export const ADMIN_WORKSPACE_HOME = '/dashboard/admin/models'

/** Route prefixes that always render the admin management shell. */
const ADMIN_WORKSPACE_PREFIXES: readonly string[] = [
  '/channels',
  '/models',
  '/dashboard/admin',
  '/users',
  '/redemption-codes',
  '/subscriptions',
  '/system-info',
  '/system-settings',
  '/dashboard/models',
  '/dashboard/users',
  '/dashboard/flow',
]

/** Route prefixes that render the customer center shell. */
const CUSTOMER_WORKSPACE_PREFIXES: readonly string[] = [
  '/dashboard/overview',
  '/dashboard',
  '/chat',
  '/chat2link',
  '/errors',
]

/**
 * Normalize a pathname for prefix matching: strip query/hash and trailing
 * slashes while keeping the root path intact.
 */
export function normalizeWorkspacePath(pathname: string): string {
  const trimmed = pathname.split('?')[0].split('#')[0]
  return trimmed.length > 1 ? trimmed.replace(/\/+$/, '') : trimmed
}

/**
 * Whether a normalized pathname sits under a route prefix. Matching is
 * segment-aware: `/models-extra` never matches the `/models` prefix and
 * `/dashboard/models-extra` never matches `/dashboard/models`.
 */
function isPathUnder(normalized: string, prefix: string): boolean {
  return normalized === prefix || normalized.startsWith(`${prefix}/`)
}

/**
 * Resolve the authenticated workspace shell for a pathname. Admin prefixes
 * are checked first so `/dashboard/models` (admin) wins over the broader
 * customer `/dashboard` prefix. Prefix matching stops at segment
 * boundaries; unknown paths fall back to the admin shell.
 */
export function resolveWorkspace(pathname: string): Workspace {
  const normalized = normalizeWorkspacePath(pathname)
  if (
    ADMIN_WORKSPACE_PREFIXES.some((prefix) => isPathUnder(normalized, prefix))
  ) {
    return 'admin'
  }
  if (
    CUSTOMER_WORKSPACE_PREFIXES.some((prefix) =>
      isPathUnder(normalized, prefix)
    )
  ) {
    return 'customer'
  }
  return 'admin'
}

/**
 * Whether the current role may see the workspace switch that opens the
 * admin shell from the customer center header.
 */
export function isAdminWorkspaceSwitchVisible(role?: number): boolean {
  return (role ?? ROLE.GUEST) >= ROLE.ADMIN
}
