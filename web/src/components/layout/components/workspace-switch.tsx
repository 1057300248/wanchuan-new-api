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
import { Link } from '@tanstack/react-router'
import { LayoutDashboard, ShieldCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { isAdminWorkspaceSwitchVisible } from '../lib/workspace'

/** Destination workspace the switch navigates to. */
export type WorkspaceSwitchTarget = 'admin' | 'customer'

type WorkspaceSwitchProps = {
  /** Current user role; the switch only renders for admin roles. */
  role?: number
  /** Workspace the switch leads to: the admin dashboard or the customer center. */
  target: WorkspaceSwitchTarget
}

/**
 * Workspace switch shown in one shell header to jump into the other shell.
 *
 * - `target='admin'` (customer center header): label "Admin Dashboard",
 *   link to the admin landing page.
 * - `target='customer'` (admin header): label "Customer Center", link to
 *   the customer landing page.
 *
 * Renders nothing for regular users so the other workspace stays
 * unreachable from either shell.
 */
export function WorkspaceSwitch(props: WorkspaceSwitchProps) {
  const { t } = useTranslation()

  if (!isAdminWorkspaceSwitchVisible(props.role)) return null

  const isAdminTarget = props.target === 'admin'
  const section = isAdminTarget ? 'models' : 'overview'
  const labelKey = isAdminTarget ? 'Admin Dashboard' : 'Customer Center'
  const Icon = isAdminTarget ? ShieldCheck : LayoutDashboard

  return (
    <Button
      size='sm'
      variant='outline'
      className='h-7 gap-1.5 px-2.5 text-xs'
      data-workspace-switch={props.target}
      render={
        <Link to='/dashboard/$section' params={{ section }} />
      }
    >
      <Icon data-icon='inline-start' />
      {t(labelKey)}
    </Button>
  )
}
