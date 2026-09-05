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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { useAccountSelf } from '../use-account-self'

function Balance() {
  const account = useAccountSelf()
  if (account.isError) return <span role='alert'>error</span>
  return <span>{account.data?.quota ?? 'loading'}</span>
}
function BalanceWithRetry() {
  const account = useAccountSelf()
  if (account.isError) {
    return (
      <button type='button' onClick={() => void account.refetch()}>
        retry account
      </button>
    )
  }
  return <span>{account.data?.quota ?? 'loading'}</span>
}
function CachedBalanceWithManualRefetch() {
  const account = useAccountSelf()
  return (
    <>
      {account.isError && <span role='alert'>Account refresh failed</span>}
      <span>{account.data?.quota ?? 'loading'}</span>
      <button type='button' onClick={() => void account.refetch()}>
        manual refetch
      </button>
    </>
  )
}
beforeEach(() => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'user', role: 1 })
})
it('shares an account request across consumers and refreshes the auth balance', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { id: 7, username: 'user', role: 1, quota: 42 },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <Balance />
      <Balance />
    </QueryClientProvider>
  )
  await waitFor(() => expect(screen.getAllByText('42')).toHaveLength(2))
  expect(get).toHaveBeenCalledTimes(1)
  expect(useAuthStore.getState().auth.user?.quota).toBe(42)
})
it('rejects a response for a different identity without replacing the current account', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { id: 8, username: 'other', role: 1, quota: 42 },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <Balance />
    </QueryClientProvider>
  )
  expect(await screen.findByRole('alert')).toBeInTheDocument()
  expect(useAuthStore.getState().auth.user?.id).toBe(7)
})
it('merges fresh balances without overwriting local language, sidebar, role, or permissions', async () => {
  useAuthStore.getState().auth.setUser({
    id: 7,
    username: 'user',
    role: 1,
    language: 'zh-CN',
    sidebar_modules: '{"pinned":["/keys"]}',
    permissions: { sidebar_settings: true },
    quota: 10,
    used_quota: 1,
    request_count: 2,
  })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        id: 7,
        username: 'user',
        role: 100,
        language: 'en',
        sidebar_modules: '{"pinned":[]}',
        permissions: {},
        quota: 99,
        used_quota: 5,
        request_count: 6,
      },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <Balance />
    </QueryClientProvider>
  )
  await waitFor(() => expect(screen.getByText('99')).toBeInTheDocument())
  const user = useAuthStore.getState().auth.user
  expect(user?.quota).toBe(99)
  expect(user?.used_quota).toBe(5)
  expect(user?.request_count).toBe(6)
  expect(user?.language).toBe('zh-CN')
  expect(user?.sidebar_modules).toBe('{"pinned":["/keys"]}')
  expect(user?.role).toBe(1)
  expect(user?.permissions).toEqual({ sidebar_settings: true })
})
it('keeps the newer auth profile when cached data remounts and the next refetch fails', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { id: 7, username: 'user', role: 1, quota: 42 },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const first = render(
    <QueryClientProvider client={client}>
      <CachedBalanceWithManualRefetch />
    </QueryClientProvider>
  )
  await waitFor(() => expect(screen.getByText('42')).toBeInTheDocument())
  expect(get).toHaveBeenCalledTimes(1)
  expect(useAuthStore.getState().auth.user?.quota).toBe(42)
  first.unmount()

  useAuthStore.getState().auth.setUser({
    id: 7,
    username: 'user',
    role: 1,
    language: 'zh-CN',
    sidebar_modules: '{"pinned":["/keys"]}',
    permissions: { sidebar_settings: true },
    quota: 99,
    used_quota: 5,
    request_count: 6,
  })
  get.mockRejectedValue(new Error('down'))
  render(
    <QueryClientProvider client={client}>
      <CachedBalanceWithManualRefetch />
    </QueryClientProvider>
  )
  await waitFor(() => expect(screen.getByText('42')).toBeInTheDocument())
  expect(get).toHaveBeenCalledTimes(1)
  expect(useAuthStore.getState().auth.user?.quota).toBe(99)

  await userEvent
    .setup()
    .click(screen.getByRole('button', { name: 'manual refetch' }))
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Account refresh failed'
  )
  const user = useAuthStore.getState().auth.user
  expect(user?.quota).toBe(99)
  expect(user?.used_quota).toBe(5)
  expect(user?.request_count).toBe(6)
  expect(user?.language).toBe('zh-CN')
  expect(user?.sidebar_modules).toBe('{"pinned":["/keys"]}')
  expect(user?.role).toBe(1)
  expect(user?.permissions).toEqual({ sidebar_settings: true })
})
it('recovers through refetch after an account request failure', async () => {
  const get = vi.spyOn(api, 'get').mockRejectedValueOnce(new Error('down'))
  get.mockResolvedValue({
    data: {
      success: true,
      data: { id: 7, username: 'user', role: 1, quota: 42 },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <BalanceWithRetry />
    </QueryClientProvider>
  )
  await userEvent
    .setup()
    .click(await screen.findByRole('button', { name: 'retry account' }))
  await waitFor(() => expect(screen.getByText('42')).toBeInTheDocument())
  expect(get).toHaveBeenCalledTimes(2)
  expect(useAuthStore.getState().auth.user?.quota).toBe(42)
})
