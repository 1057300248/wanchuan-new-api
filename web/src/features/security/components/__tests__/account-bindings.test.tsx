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
import { render, screen, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { UserProfile } from '@/features/profile/types'

import { AccountBindings } from '../account-bindings'

const { useStatusMock } = vi.hoisted(() => ({
  useStatusMock: vi.fn(),
}))

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => useStatusMock(),
}))

const { useAccountSecurityMock } = vi.hoisted(() => {
  // Stable identities: dialogs depend on these functions/objects in effects,
  // so returning fresh values per render would loop.
  const security = {
    pending: false,
    error: null,
    sessionKey: 'test-session',
    showVerification: false,
    verificationDialogProps: {},
    run: vi.fn(),
    verify: vi.fn(),
    cancel: vi.fn(),
  }
  return { useAccountSecurityMock: () => security }
})

vi.mock('../../hooks/use-account-security', () => ({
  useAccountSecurity: useAccountSecurityMock,
}))

const profile = {
  id: 1,
  username: 'tester',
  display_name: 'Tester',
  role: 1,
  email: 'tester@example.com',
  group: 'default',
  quota: 0,
  used_quota: 0,
  request_count: 0,
  status: 1,
  aff_count: 0,
} as unknown as UserProfile

function statusWith(serverAddress: string) {
  return {
    status: {
      github_oauth: true,
      linuxdo_oauth: true,
      server_address: serverAddress,
    },
    loading: false,
    error: null,
  }
}

beforeEach(() => {
  useStatusMock.mockReturnValue(statusWith(window.location.origin))
})

describe('AccountBindings entry availability', () => {
  it('offers unbound OAuth bindings on the ServerAddress origin', () => {
    render(<AccountBindings profile={profile} onUpdate={vi.fn()} />)

    const githubRow = screen.getByText('GitHub').closest('li')
    expect(githubRow).not.toBeNull()
    expect(
      within(githubRow as HTMLElement).getByRole('button', { name: 'Bind' })
    ).toBeVisible()
    expect(screen.getByText('LinuxDO')).toBeVisible()
  })

  it('hides unbound OAuth bindings on another entry but keeps email', () => {
    useStatusMock.mockReturnValue(statusWith('https://chuanapi.com'))
    render(<AccountBindings profile={profile} onUpdate={vi.fn()} />)

    expect(screen.queryByText('GitHub')).not.toBeInTheDocument()
    expect(screen.queryByText('LinuxDO')).not.toBeInTheDocument()
    const emailRow = screen.getByText('Email').closest('li')
    expect(emailRow).not.toBeNull()
    expect(
      within(emailRow as HTMLElement).getByRole('button', { name: 'Change' })
    ).toBeVisible()
  })
})
