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
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { fetchTokenKey, getApiKeys, searchApiKeys } from '../../api'
import type { ApiKey } from '../../types'
import { ApiKeysProvider } from '../api-keys-provider'
import { ApiKeysTable } from '../api-keys-table'

vi.mock('../../api', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  getApiKeys: vi.fn(),
  searchApiKeys: vi.fn(),
  fetchTokenKey: vi.fn(),
}))

const storedKey: ApiKey = {
  id: 7,
  name: 'wanchuan-key',
  key: 'abc123',
  status: 1,
  remain_quota: 500_000,
  used_quota: 100_000,
  unlimited_quota: false,
  expired_time: -1,
  created_time: 1_700_000_000,
  accessed_time: 1_700_000_100,
  group: 'default',
  auto_groups: null,
  cross_group_retry: false,
  model_limits_enabled: false,
  model_limits: '',
  allow_ips: '',
}

function KeysHarness() {
  return (
    <ApiKeysProvider>
      <ApiKeysTable />
    </ApiKeysProvider>
  )
}

function KeysLayout() {
  return <Outlet />
}

function renderKeysTable() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const rootRoute = createRootRoute({ component: KeysLayout })
  const authRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '_authenticated',
    component: KeysLayout,
  })
  const keysRoute = createRoute({
    getParentRoute: () => authRoute,
    path: 'keys/',
    component: KeysHarness,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([authRoute.addChildren([keysRoute])]),
    history: createMemoryHistory({ initialEntries: ['/_authenticated/keys/'] }),
  })
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

describe('api keys table state', () => {
  beforeEach(() => {
    vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
      if (url === '/api/user/self/groups') {
        return {
          data: {
            success: true,
            data: { default: { desc: 'Standard access', ratio: 1 } },
          },
        }
      }
      return { data: { success: true, data: [] } }
    })
    vi.mocked(searchApiKeys).mockResolvedValue({
      success: true,
      data: { items: [], total: 0, page: 1, page_size: 20 },
    })
  })

  test('shows load failure with retry instead of disguising it as an empty list', async () => {
    vi.mocked(getApiKeys).mockResolvedValue({
      success: false,
      message: 'keys down',
    })
    renderKeysTable()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Failed to load data')
    expect(screen.queryByText('No API Keys Found')).not.toBeInTheDocument()

    vi.mocked(getApiKeys).mockResolvedValue({
      success: true,
      data: { items: [storedKey], total: 1, page: 1, page_size: 20 },
    })
    await userEvent
      .setup()
      .click(within(alert).getByRole('button', { name: 'Retry' }))

    expect(await screen.findAllByText('wanchuan-key')).not.toHaveLength(0)
    await waitFor(() =>
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    )
  })

  test('keeps the stored key masked until the full key is explicitly requested', async () => {
    vi.mocked(getApiKeys).mockResolvedValue({
      success: true,
      data: { items: [storedKey], total: 1, page: 1, page_size: 20 },
    })
    vi.mocked(fetchTokenKey).mockResolvedValue({
      success: true,
      data: { key: 'full-secret-value' },
    })
    renderKeysTable()

    expect(await screen.findAllByText('wanchuan-key')).not.toHaveLength(0)
    expect(screen.getAllByText('sk-abc123').length).toBeGreaterThan(0)
    expect(screen.queryByText('sk-full-secret-value')).not.toBeInTheDocument()
    expect(fetchTokenKey).not.toHaveBeenCalled()

    await userEvent.setup().click(screen.getAllByText('sk-abc123')[0])

    expect(fetchTokenKey).toHaveBeenCalledWith(7)
    expect(
      await screen.findByDisplayValue('sk-full-secret-value')
    ).toBeInTheDocument()
  })

  test('opens the full key with keyboard Enter and returns focus to the trigger on Escape', async () => {
    vi.mocked(getApiKeys).mockResolvedValue({
      success: true,
      data: { items: [storedKey], total: 1, page: 1, page_size: 20 },
    })
    vi.mocked(fetchTokenKey).mockResolvedValue({
      success: true,
      data: { key: 'full-secret-value' },
    })
    renderKeysTable()

    const trigger = await screen.findByRole('button', { name: 'sk-abc123' })
    trigger.focus()
    expect(trigger).toHaveFocus()

    await userEvent.setup().keyboard('{Enter}')

    expect(fetchTokenKey).toHaveBeenCalledWith(7)
    const fullKeyInput = await screen.findByDisplayValue('sk-full-secret-value')
    expect(fullKeyInput).toBeInTheDocument()

    await userEvent.setup().keyboard('{Escape}')
    await waitFor(() => expect(trigger).toHaveFocus())
  })
})
