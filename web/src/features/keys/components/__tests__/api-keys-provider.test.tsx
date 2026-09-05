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
import {
  QueryClient,
  QueryClientProvider,
  useQuery,
} from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { getApiKeys } from '../../api'
import { ApiKeysProvider, useApiKeys } from '../api-keys-provider'

vi.mock('../../api', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  getApiKeys: vi.fn(),
}))

function OverviewKeysReader() {
  const userId = useAuthStore((s) => s.auth.user?.id)
  const overviewKeys = useQuery({
    queryKey: ['dashboard', userId, 'overview', 'api-keys'],
    enabled: Boolean(userId),
    queryFn: async () => {
      const result = await getApiKeys({ p: 1, size: 10 })
      if (!result.success) throw new Error('keys down')
      return result.data?.items ?? []
    },
    staleTime: 60 * 1000,
  })
  return (
    <span>
      {overviewKeys.data?.map((key) => key.name).join(',') ?? 'loading'}
    </span>
  )
}

function RefreshButton() {
  const { triggerRefresh } = useApiKeys()
  return (
    <button type='button' onClick={triggerRefresh}>
      refresh keys
    </button>
  )
}

beforeEach(() => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'user', role: 1 })
})

test('refreshing keys invalidates the overview api-keys cache so the overview updates', async () => {
  vi.mocked(getApiKeys).mockResolvedValue({
    success: true,
    data: {
      items: [
        {
          id: 1,
          name: 'old-key',
          key: 'old',
          status: 1,
          remain_quota: 10,
          used_quota: 0,
          unlimited_quota: false,
          expired_time: -1,
          created_time: 1,
          accessed_time: 1,
          group: '',
          auto_groups: null,
          cross_group_retry: false,
          model_limits_enabled: false,
          model_limits: '',
          allow_ips: '',
        },
      ],
      total: 1,
      page: 1,
      page_size: 10,
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <ApiKeysProvider>
        <OverviewKeysReader />
        <RefreshButton />
      </ApiKeysProvider>
    </QueryClientProvider>
  )

  expect(await screen.findByText('old-key')).toBeInTheDocument()

  vi.mocked(getApiKeys).mockResolvedValue({
    success: true,
    data: {
      items: [
        {
          id: 2,
          name: 'new-key',
          key: 'new',
          status: 1,
          remain_quota: 10,
          used_quota: 0,
          unlimited_quota: false,
          expired_time: -1,
          created_time: 1,
          accessed_time: 1,
          group: '',
          auto_groups: null,
          cross_group_retry: false,
          model_limits_enabled: false,
          model_limits: '',
          allow_ips: '',
        },
      ],
      total: 1,
      page: 1,
      page_size: 10,
    },
  })
  await userEvent
    .setup()
    .click(screen.getByRole('button', { name: 'refresh keys' }))

  await waitFor(() => expect(screen.getByText('new-key')).toBeInTheDocument())
})
