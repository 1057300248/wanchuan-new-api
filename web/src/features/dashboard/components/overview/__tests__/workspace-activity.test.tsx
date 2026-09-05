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
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { screen, render, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { WorkspaceActivity } from '../workspace-activity'

beforeEach(() => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'tester', role: 1 })
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date(2026, 5, 15, 12, 0, 0, 0))
})
afterEach(() => {
  vi.useRealTimers()
})
function renderActivity() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const route = createRootRoute({ component: WorkspaceActivity })
  const router = createRouter({
    routeTree: route,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}
it('shows empty usage without fabricating a chart or requests', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: [] } })
  renderActivity()
  expect(await screen.findAllByText('No recent usage')).toHaveLength(2)
  expect(screen.queryByRole('img')).not.toBeInTheDocument()
})
it('shows API failure and retries successfully using the retry button', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: false } })
  renderActivity()
  expect(await screen.findAllByRole('alert')).toHaveLength(2)
  get.mockResolvedValue({ data: { success: true, data: [] } })
  await userEvent.click(screen.getAllByRole('button', { name: 'Retry' })[0])
  expect(await screen.findByText('No recent usage')).toBeInTheDocument()
})
it('switches time range and metric and displays aggregated usage accessibly', async () => {
  const todayMidnight = new Date(2026, 5, 15, 0, 0, 0, 0).getTime() / 1000
  const morning = todayMidnight + 2 * 3600
  const evening = todayMidnight + 20 * 3600
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: String(url).startsWith('/api/data')
        ? [
            { created_at: morning, quota: 20, count: 3 },
            { created_at: evening, quota: 40, count: 2 },
          ]
        : { items: [] },
    },
  }))
  renderActivity()
  expect(
    await screen.findByRole('img', { name: 'Consumption' })
  ).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Consumption' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Requests' }))
  expect(screen.getByRole('button', { name: 'Requests' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(screen.getByRole('img', { name: 'Requests' })).toBeInTheDocument()
  await userEvent.click(screen.getByText('Details'))
  expect(screen.getByText('5')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '30 days' }))
  expect(screen.getByRole('button', { name: '30 days' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  await waitFor(() =>
    expect(
      get.mock.calls.filter(([url]) => String(url).startsWith('/api/data'))
    ).toHaveLength(2)
  )
})

it('distinguishes failed calls from consumption and omits non-request ledger entries', async () => {
  const todayMidnight = new Date(2026, 5, 15, 0, 0, 0, 0).getTime() / 1000
  const now = todayMidnight + 12 * 3600
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    const raw = String(url)
    if (raw.startsWith('/api/data')) {
      return { data: { success: true, data: [] } }
    }
    if (raw.includes('type=2')) {
      return {
        data: {
          success: true,
          data: {
            items: [
              {
                id: 2,
                user_id: 7,
                created_at: now,
                type: 2,
                content: '',
                model_name: 'successful-model',
                quota: 10,
                use_time: 1,
              },
              {
                id: 3,
                user_id: 7,
                created_at: now,
                type: 1,
                content: '',
                model_name: 'topup-not-a-request',
                quota: 0,
                use_time: 0,
              },
            ],
          },
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          items: [
            {
              id: 1,
              user_id: 7,
              created_at: now - 60,
              type: 5,
              content: '',
              model_name: 'failed-model',
              quota: 4,
              use_time: 2,
            },
            {
              id: 2,
              user_id: 7,
              created_at: now,
              type: 2,
              content: '',
              model_name: 'successful-model',
              quota: 10,
              use_time: 1,
            },
          ],
        },
      },
    }
  })
  renderActivity()
  expect(await screen.findByText('failed-model')).toBeInTheDocument()
  expect(screen.getByText('Error')).toBeInTheDocument()
  expect(screen.getByText('Success')).toBeInTheDocument()
  expect(screen.queryByText('topup-not-a-request')).not.toBeInTheDocument()
  expect(screen.getAllByRole('columnheader')).toHaveLength(4)
  expect(screen.getAllByRole('cell')).toHaveLength(8)
  const rows = screen.getAllByRole('row')
  expect(rows[1]).toHaveTextContent('successful-model')
  expect(rows[2]).toHaveTextContent('failed-model')
})
