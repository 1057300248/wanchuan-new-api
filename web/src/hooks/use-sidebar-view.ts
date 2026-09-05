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
import { useLocation } from '@tanstack/react-router'
import { BookOpen, Tags } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { resolveSidebarView } from '@/components/layout/lib/sidebar-view-registry'
import type { NavGroup, ResolvedSidebarView } from '@/components/layout/types'
import { useThemeCustomization } from '@/context/theme-customization-provider'
import { MARKETING_HOME_URL, QUICKSTART_DOCS_URL } from '@/lib/public-site'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { useSidebarConfig } from './use-sidebar-config'
import { useSidebarData } from './use-sidebar-data'

/** Sentinel key used for the root navigation in animation `key=` props */
const ROOT_VIEW_KEY = '__root'

/**
 * Resolve the active sidebar view for the current location.
 *
 * - Returns the matching nested {@link SidebarView} (with its nav
 *   groups) when the URL belongs to a registered drill-in workspace.
 * - Otherwise returns the root navigation, narrowed by:
 *     · admin-only group visibility (role-based);
 *     · `useSidebarConfig` (admin × user `sidebar_modules` overlay).
 *
 * Nested views are intentionally NOT passed through `useSidebarConfig`
 * — those filters target known dashboard URLs only, and gating is
 * already enforced at the route level (`beforeLoad` redirects).
 */
export function useSidebarView(): ResolvedSidebarView {
  const { t } = useTranslation()
  const pathname = useLocation({ select: (l) => l.pathname })
  const userRole = useAuthStore((s) => s.auth.user?.role)
  const { customization } = useThemeCustomization()
  const rootSidebarData = useSidebarData()
  const configFilteredRoot = useSidebarConfig(rootSidebarData.navGroups)

  const rootNavGroups = useMemo<NavGroup[]>(() => {
    const role = userRole ?? ROLE.GUEST
    const isAdmin = role >= ROLE.ADMIN
    const groups = configFilteredRoot
      .filter((group) => (group.id === 'admin' ? isAdmin : true))
      .map((group) => {
        const items = group.items.filter(
          (item) => item.requiredRole === undefined || role >= item.requiredRole
        )
        return items.length === group.items.length ? group : { ...group, items }
      })
    if (customization.preset !== 'wanchuan' || isAdmin) return groups

    // Reorder only after permission and module filters; hidden links stay hidden.
    const general = groups.find((group) => group.id === 'general')
    const personal = groups.find((group) => group.id === 'personal')
    const wallet = personal?.items.find((item) => item.url === '/wallet')
    const primaryOrder = [
      '/dashboard/overview',
      '/keys',
      '/usage-logs/common',
      '/wallet',
      '/dashboard/models',
      '/usage-logs/task',
    ]
    const workspace: NavGroup = {
      id: 'general',
      title: t('Console'),
      items: [...(general?.items ?? []), ...(wallet ? [wallet] : [])].sort(
        (left, right) =>
          primaryOrder.indexOf(String(left.url)) -
          primaryOrder.indexOf(String(right.url))
      ),
    }
    return [
      workspace,
      ...groups
        .filter((group) => group.id !== 'general' && group.id !== 'personal')
        .map((group) => ({
          ...group,
          items: group.items.map((item) =>
            item.url === '/playground'
              ? {
                  ...item,
                  url: `${MARKETING_HOME_URL}/playground/`,
                  external: true,
                }
              : item
          ),
        })),
      {
        id: 'resources',
        title: t('Resources'),
        items: [
          {
            title: t('Pricing'),
            url: `${MARKETING_HOME_URL}/model-pricing/`,
            external: true,
            icon: Tags,
          },
          {
            title: t('Documentation'),
            url: QUICKSTART_DOCS_URL,
            external: true,
            icon: BookOpen,
          },
        ],
      },
      ...(personal
        ? [
            {
              ...personal,
              items: personal.items.filter((item) => item.url !== '/wallet'),
            },
          ]
        : []),
    ].filter((group) => group.items.length > 0)
  }, [configFilteredRoot, userRole, customization.preset, t])

  const view = resolveSidebarView(pathname)

  if (view) {
    return {
      key: view.id,
      view,
      navGroups: view.getNavGroups(t),
    }
  }

  return {
    key: ROOT_VIEW_KEY,
    view: null,
    navGroups: rootNavGroups,
  }
}
