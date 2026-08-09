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
import { LanguageSwitcher } from '@/components/language-switcher'
import { NotificationPopover } from '@/components/notification-popover'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { useNotifications } from '@/hooks/use-notifications'
import { useAuthStore } from '@/stores/auth-store'

import { Header } from './header'
import { SystemBrand } from './system-brand'
import { WorkspaceSwitch } from './workspace-switch'

/**
 * Customer center header.
 *
 * Customer-focused by design: no dynamic top nav, no search, no config
 * drawer, no admin entries. Admins only get a single explicit switch that
 * opens the management console.
 */
export function CustomerCenterHeader() {
  const notifications = useNotifications()
  const role = useAuthStore((state) => state.auth.user?.role)

  return (
    <Header>
      <SystemBrand variant='inline' />
      <WorkspaceSwitch role={role} target='admin' />
      <div className='ms-auto flex items-center gap-1 sm:gap-2'>
        <NotificationPopover
          open={notifications.popoverOpen}
          onOpenChange={notifications.setPopoverOpen}
          unreadCount={notifications.unreadCount}
          activeTab={notifications.activeTab}
          onTabChange={notifications.setActiveTab}
          notice={notifications.notice}
          announcements={notifications.announcements}
          loading={notifications.loading}
        />
        <LanguageSwitcher />
        <ProfileDropdown />
      </div>
    </Header>
  )
}
