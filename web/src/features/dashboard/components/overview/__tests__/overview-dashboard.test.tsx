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
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { copyToClipboard } from '@/lib/copy-to-clipboard'
import { useAuthStore } from '@/stores/auth-store'

import { fetchTokenKey, getApiKeys } from '../../../../keys/api'
import { OverviewDashboard } from '../overview-dashboard'

vi.mock('../../../../keys/api', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  getApiKeys: vi.fn(),
  fetchTokenKey: vi.fn(),
}))

vi.mock('@/lib/copy-to-clipboard', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  copyToClipboard: vi.fn(async () => true),
}))

const nowSeconds = () => Math.floor(Date.now() / 1000)

function validKey(overrides = {}) {
  return {
    id: 11,
    name: 'valid-key',
    key: 'abc123',
    status: 1,
    remain_quota: 500_000,
    used_quota: 0,
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
    ...overrides,
  }
}

function mockBaseApis() {
  vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
    if (url === '/api/user/self') {
      return {
        data: {
          success: true,
          data: {
            id: 7,
            username: 'user',
            role: 1,
            quota: 500_000,
            used_quota: 0,
            request_count: 0,
          },
        },
      }
    }
    if (url === '/api/user/models') {
      return { data: { success: true, data: ['gpt-4'] } }
    }
    if (url === '/api/status') {
      return { data: { data: {} } }
    }
    if (String(url).startsWith('/api/data')) {
      return { data: { success: true, data: [] } }
    }
    if (String(url).startsWith('/api/log')) {
      return { data: { success: true, data: { items: [] } } }
    }
    return { data: { success: true, data: [] } }
  })
}

function renderOverview() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={client}>
        <OverviewDashboard />
      </QueryClientProvider>
    ),
  })
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => (
      <QueryClientProvider client={client}>
        <OverviewDashboard />
      </QueryClientProvider>
    ),
  })
  const keysRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/keys',
    component: () => <div>keys</div>,
  })
  const walletRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/wallet',
    component: () => <div>wallet</div>,
  })
  const channelsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/channels',
    component: () => <div>channels</div>,
  })
  const logsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/usage-logs',
    component: () => <div>logs</div>,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([
      indexRoute,
      keysRoute,
      walletRoute,
      channelsRoute,
      logsRoute,
    ]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  return { view: render(<RouterProvider router={router} />), client, router }
}

describe('overview dashboard request preview', () => {
  beforeEach(() => {
    useAuthStore.getState().auth.setUser({ id: 7, username: 'user', role: 1 })
    window.localStorage.clear()
    mockBaseApis()
  })

  test('selects only an active, unexpired, funded, model-matching key and keeps the preview masked', async () => {
    vi.mocked(getApiKeys).mockResolvedValue({
      success: true,
      data: {
        items: [
          validKey({
            id: 1,
            name: 'expired-key',
            expired_time: nowSeconds() - 60,
          }),
          validKey({ id: 2, name: 'exhausted-key', remain_quota: 0 }),
          validKey({
            id: 3,
            name: 'mismatched-key',
            model_limits_enabled: true,
            model_limits: 'other-model',
          }),
          validKey({ id: 11, name: 'valid-key' }),
        ],
        total: 4,
        page: 1,
        page_size: 10,
      },
    })
    renderOverview()

    expect(await screen.findAllByText('valid-key')).not.toHaveLength(0)
    expect(screen.queryByText('expired-key')).not.toBeInTheDocument()
    expect(screen.queryByText('exhausted-key')).not.toBeInTheDocument()
    expect(screen.queryByText('mismatched-key')).not.toBeInTheDocument()
    const preview = await screen.findByLabelText('First API request')
    expect(preview).toHaveTextContent('sk-...')
    expect(preview).not.toHaveTextContent('sk-abc123')
    expect(preview).toHaveTextContent('gpt-4')
    expect(vi.mocked(fetchTokenKey)).not.toHaveBeenCalled()
  })

  test('fetches the full key only on explicit copy and quotes shell arguments safely', async () => {
    vi.mocked(getApiKeys).mockResolvedValue({
      success: true,
      data: { items: [validKey()], total: 1, page: 1, page_size: 10 },
    })
    vi.mocked(fetchTokenKey).mockResolvedValue({
      success: true,
      data: { key: "a'b" },
    })
    renderOverview()

    const preview = await screen.findByLabelText('First API request')
    expect(preview).toHaveTextContent('sk-...')

    const copyButton = await screen.findByRole('button', {
      name: 'Copy ready-to-run curl',
    })
    await userEvent.setup().click(copyButton)

    await waitFor(() =>
      expect(vi.mocked(fetchTokenKey)).toHaveBeenCalledWith(11)
    )
    await waitFor(() =>
      expect(vi.mocked(copyToClipboard)).toHaveBeenCalledTimes(1)
    )
    const copied = vi.mocked(copyToClipboard).mock.calls[0][0] as string
    expect(copied).toContain("sk-a'\\''b")
    expect(copied).toContain('Authorization: Bearer')
    expect(await screen.findByLabelText('First API request')).toHaveTextContent(
      'sk-...'
    )
  })

  test('shows no guessed model when the model list is empty', async () => {
    vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
      if (url === '/api/user/self') {
        return {
          data: {
            success: true,
            data: {
              id: 7,
              username: 'user',
              role: 1,
              quota: 1,
              used_quota: 0,
              request_count: 0,
            },
          },
        }
      }
      if (url === '/api/user/models') {
        return { data: { success: true, data: [] } }
      }
      if (url === '/api/status') return { data: { data: {} } }
      if (String(url).startsWith('/api/data')) {
        return { data: { success: true, data: [] } }
      }
      if (String(url).startsWith('/api/log')) {
        return { data: { success: true, data: { items: [] } } }
      }
      return { data: { success: true, data: [] } }
    })
    vi.mocked(getApiKeys).mockResolvedValue({
      success: true,
      data: { items: [validKey()], total: 1, page: 1, page_size: 10 },
    })
    renderOverview()

    expect(await screen.findAllByText('No models available')).not.toHaveLength(
      0
    )
    expect(screen.queryByLabelText('First API request')).not.toBeInTheDocument()
    expect(vi.mocked(fetchTokenKey)).not.toHaveBeenCalled()
  })

  test('keeps the masked preview after a keys failure and recovers on retry', async () => {
    vi.mocked(getApiKeys).mockResolvedValueOnce({
      success: false,
      message: 'keys down',
    })
    renderOverview()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Failed to load data')

    vi.mocked(getApiKeys).mockResolvedValue({
      success: true,
      data: { items: [validKey()], total: 1, page: 1, page_size: 10 },
    })
    await userEvent
      .setup()
      .click(within(alert).getByRole('button', { name: 'Retry' }))

    expect(await screen.findAllByText('valid-key')).not.toHaveLength(0)
    expect(await screen.findByLabelText('First API request')).toHaveTextContent(
      'sk-...'
    )
  })

  test('copies the base URL without reading the full key', async () => {
    vi.mocked(getApiKeys).mockResolvedValue({
      success: true,
      data: { items: [validKey()], total: 1, page: 1, page_size: 10 },
    })
    renderOverview()

    const baseUrlButton = await screen.findByRole('button', {
      name: 'Copy Base URL',
    })
    await userEvent.setup().click(baseUrlButton)

    await waitFor(() =>
      expect(vi.mocked(copyToClipboard)).toHaveBeenCalledTimes(1)
    )
    const copied = vi.mocked(copyToClipboard).mock.calls[0][0] as string
    expect(copied).toContain('/v1')
    expect(copied).not.toContain('chat/completions')
    expect(vi.mocked(fetchTokenKey)).not.toHaveBeenCalled()
  })
})
