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
import type { TFunction } from 'i18next'
import {
  Activity,
  FileText,
  FlaskConical,
  Key,
  Share2,
  User,
  Wallet,
} from 'lucide-react'

import type { NavGroup } from '../types'

/**
 * Navigation shown inside the customer center sidebar.
 *
 * Deliberately customer-only: keys, usage, wallet, referral and profile.
 * No channels/models/users/system entries live here — admins reach those
 * through the workspace switch instead.
 */
export function buildCustomerNavGroups(t: TFunction): NavGroup[] {
  return [
    {
      id: 'customer-general',
      title: t('General'),
      items: [
        {
          title: t('Overview'),
          url: '/dashboard/overview',
          icon: Activity,
        },
        {
          title: t('API Keys'),
          url: '/dashboard/keys',
          icon: Key,
        },
        {
          title: t('Usage Logs'),
          url: '/dashboard/logs/common',
          activeUrls: [
            '/dashboard/logs/common',
            '/dashboard/logs/task',
            '/dashboard/logs/drawing',
          ],
          icon: FileText,
        },
        {
          title: t('Playground'),
          url: '/dashboard/playground',
          icon: FlaskConical,
        },
      ],
    },
    {
      id: 'customer-personal',
      title: t('Personal'),
      items: [
        {
          title: t('Wallet'),
          url: '/dashboard/wallet',
          icon: Wallet,
        },
        {
          title: t('Referral Program'),
          url: '/dashboard/referrals',
          activeUrls: ['/dashboard/referrals'],
          icon: Share2,
        },
        {
          title: t('Profile'),
          url: '/dashboard/settings',
          icon: User,
        },
      ],
    },
  ]
}
