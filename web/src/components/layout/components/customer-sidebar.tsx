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
import { useTranslation } from 'react-i18next'

import { Sidebar, SidebarContent, SidebarRail } from '@/components/ui/sidebar'
import { useLayout } from '@/context/layout-provider'

import { buildCustomerNavGroups } from '../lib/customer-nav'
import { NavGroup } from './nav-group'

/**
 * Customer center sidebar.
 *
 * Reuses the existing sidebar primitives and `NavGroup` rendering (active
 * states, collapsed tooltips, mobile drawer close) with a customer-only
 * navigation built by {@link buildCustomerNavGroups}.
 */
export function CustomerSidebar() {
  const { t } = useTranslation()
  const { collapsible, variant } = useLayout()
  const navGroups = buildCustomerNavGroups(t)

  return (
    <Sidebar collapsible={collapsible} variant={variant}>
      <SidebarContent className='py-2'>
        {navGroups.map((group) => (
          <NavGroup key={group.id ?? group.title} {...group} />
        ))}
      </SidebarContent>
      <SidebarRail />
    </Sidebar>
  )
}
