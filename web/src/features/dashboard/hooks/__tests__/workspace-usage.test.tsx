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
import { renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { useWorkspaceUsage } from '../use-workspace-usage'

function createClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
}

function wrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
}

const originalTz = process.env.TZ

beforeEach(() => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'user', role: 1 })
  vi.restoreAllMocks()
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date(2026, 5, 15, 12, 0, 0, 0))
})

afterEach(() => {
  vi.useRealTimers()
  if (process.env.TZ !== originalTz) {
    if (originalTz === undefined) delete process.env.TZ
    else process.env.TZ = originalTz
  }
})

it('builds 7 local calendar day buckets and fills empty days with zero', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [] },
  })
  const client = createClient()
  const { result } = renderHook(() => useWorkspaceUsage(7), {
    wrapper: wrapper(client),
  })

  await waitFor(() => expect(result.current.query.isSuccess).toBe(true))
  expect(result.current.buckets).toHaveLength(7)
  for (const bucket of result.current.buckets) {
    expect(bucket.quota).toBe(0)
    expect(bucket.count).toBe(0)
    expect(bucket.tokens).toBe(0)
  }
  const expectedStamps = [9, 10, 11, 12, 13, 14, 15].map(
    (day) => new Date(2026, 5, day, 0, 0, 0, 0).getTime() / 1000
  )
  expect(result.current.buckets.map((bucket) => bucket.timestamp)).toEqual(
    expectedStamps
  )
  expect(result.current.period.start_timestamp).toBe(expectedStamps[0])
  expect(result.current.period.end_timestamp).toBe(
    new Date(2026, 5, 16, 0, 0, 0, 0).getTime() / 1000 - 1
  )
  expect(get).toHaveBeenCalledTimes(1)
})

it('aggregates same-day hourly rows into the local today bucket', async () => {
  const todayMidnight = new Date(2026, 5, 15, 0, 0, 0, 0).getTime() / 1000
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: [
        {
          created_at: todayMidnight + 3600,
          quota: 20,
          count: 3,
          token_used: 100,
        },
        {
          created_at: todayMidnight + 7200,
          quota: 40,
          count: 2,
          token_used: 200,
        },
      ],
    },
  })
  const client = createClient()
  const { result } = renderHook(() => useWorkspaceUsage(7), {
    wrapper: wrapper(client),
  })

  await waitFor(() => expect(result.current.query.isSuccess).toBe(true))
  const today = result.current.buckets.find(
    (bucket) => bucket.timestamp === todayMidnight
  )
  expect(today?.quota).toBe(60)
  expect(today?.count).toBe(5)
  expect(today?.tokens).toBe(300)
})

it('supports a 30-day range with 30 buckets', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [] },
  })
  const client = createClient()
  const { result } = renderHook(() => useWorkspaceUsage(30), {
    wrapper: wrapper(client),
  })

  await waitFor(() => expect(result.current.query.isSuccess).toBe(true))
  expect(result.current.buckets).toHaveLength(30)
  expect(result.current.period.start_timestamp).toBe(
    new Date(2026, 4, 17, 0, 0, 0, 0).getTime() / 1000
  )
  expect(result.current.period.end_timestamp).toBe(
    new Date(2026, 5, 16, 0, 0, 0, 0).getTime() / 1000 - 1
  )
  expect(result.current.buckets[0].timestamp).toBe(
    result.current.period.start_timestamp
  )
  expect(result.current.buckets[29].timestamp).toBe(
    new Date(2026, 5, 15, 0, 0, 0, 0).getTime() / 1000
  )
  expect(get).toHaveBeenCalledTimes(1)
})

it('splits a DST-extended 30 local-day range into two non-overlapping requests', async () => {
  process.env.TZ = 'America/New_York'
  vi.setSystemTime(new Date('2026-11-05T12:00:00-05:00'))
  let callIndex = 0
  const get = vi.spyOn(api, 'get').mockImplementation(async (_url, config) => {
    callIndex += 1
    const params = (
      config as { params: { start_timestamp: number; end_timestamp: number } }
    ).params
    if (callIndex === 1) {
      return {
        data: {
          success: true,
          data: [
            {
              created_at: params.start_timestamp + 3600,
              quota: 11,
              count: 1,
              token_used: 10,
            },
            {
              created_at: params.end_timestamp,
              quota: 22,
              count: 2,
              token_used: 20,
            },
          ],
        },
      }
    }
    return {
      data: {
        success: true,
        data: [],
      },
    }
  })
  const client = createClient()
  const { result } = renderHook(() => useWorkspaceUsage(30), {
    wrapper: wrapper(client),
  })

  await waitFor(() => expect(result.current.query.isSuccess).toBe(true))
  expect(result.current.buckets).toHaveLength(30)
  expect(get).toHaveBeenCalledTimes(2)
  const ranges = get.mock.calls.map(
    (call) =>
      (
        call[1] as {
          params: { start_timestamp: number; end_timestamp: number }
        }
      ).params
  )
  expect(ranges[0].start_timestamp).toBe(result.current.period.start_timestamp)
  expect(ranges[1].end_timestamp).toBe(result.current.period.end_timestamp)
  expect(ranges[1].start_timestamp).toBe(ranges[0].end_timestamp + 1)
  expect(ranges[0].end_timestamp).toBeLessThan(ranges[1].start_timestamp)
  expect(
    ranges[0].end_timestamp - ranges[0].start_timestamp
  ).toBeLessThanOrEqual(2592000)
  expect(
    ranges[1].end_timestamp - ranges[1].start_timestamp
  ).toBeLessThanOrEqual(2592000)
  expect(
    result.current.period.end_timestamp - result.current.period.start_timestamp
  ).toBeGreaterThan(2592000)
  const quotas = result.current.buckets.map((bucket) => bucket.quota)
  expect(quotas).toContain(11)
  expect(quotas).toContain(22)
  expect(quotas.reduce((sum, quota) => sum + quota, 0)).toBe(33)
})

it('surfaces a failed usage request without fabricating buckets', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: false },
  })
  const client = createClient()
  const { result } = renderHook(() => useWorkspaceUsage(7), {
    wrapper: wrapper(client),
  })

  await waitFor(() => expect(result.current.query.isError).toBe(true))
  expect(result.current.buckets).toHaveLength(7)
})

it('shares one usage request across simultaneous consumers', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [] },
  })
  const client = createClient()
  const hookWrapper = wrapper(client)
  const first = renderHook(() => useWorkspaceUsage(7), {
    wrapper: hookWrapper,
  })
  const second = renderHook(() => useWorkspaceUsage(7), {
    wrapper: hookWrapper,
  })

  await waitFor(() => expect(first.result.current.query.isSuccess).toBe(true))
  await waitFor(() => expect(second.result.current.query.isSuccess).toBe(true))
  expect(get).toHaveBeenCalledTimes(1)
})

it('isolates the usage cache by user identity', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [] },
  })
  const client = createClient()
  const { result } = renderHook(() => useWorkspaceUsage(7), {
    wrapper: wrapper(client),
  })
  await waitFor(() => expect(result.current.query.isSuccess).toBe(true))
  expect(get).toHaveBeenCalledTimes(1)

  useAuthStore.getState().auth.setUser({ id: 8, username: 'other', role: 1 })
  const { result: other } = renderHook(() => useWorkspaceUsage(7), {
    wrapper: wrapper(client),
  })
  await waitFor(() => expect(other.current.query.isSuccess).toBe(true))
  expect(get).toHaveBeenCalledTimes(2)
})
