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
import { AnimatedOutlet } from '@/components/page-transition'
import { SkipToMain } from '@/components/skip-to-main'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import { LayoutProvider } from '@/context/layout-provider'
import { getCookie } from '@/lib/cookies'
import { cn } from '@/lib/utils'

import { CustomerCenterHeader } from './customer-center-header'
import { CustomerSidebar } from './customer-sidebar'

type CustomerCenterLayoutProps = {
  children?: React.ReactNode
}

/**
 * Customer center shell: header + sidebar built around real user features
 * (overview, keys, usage logs, wallet, referral, profile, playground).
 * Rendered for every authenticated customer path regardless of role.
 */
export function CustomerCenterLayout(props: CustomerCenterLayoutProps) {
  const defaultOpen = getCookie('sidebar_state') !== 'false'

  return (
    <LayoutProvider>
      <SidebarProvider defaultOpen={defaultOpen} className='flex-col'>
        <SkipToMain />
        <CustomerCenterHeader />
        <div className='flex min-h-0 w-full flex-1'>
          <CustomerSidebar />
          <SidebarInset
            id='content'
            className={cn(
              '@container/content',
              'h-[calc(100svh-var(--app-header-height,0px))]',
              'min-h-0 overflow-hidden',
              'peer-data-[variant=inset]:h-[calc(100svh-var(--app-header-height,0px)-(var(--spacing)*4))]'
            )}
          >
            {props.children ?? <AnimatedOutlet />}
          </SidebarInset>
        </div>
      </SidebarProvider>
    </LayoutProvider>
  )
}
