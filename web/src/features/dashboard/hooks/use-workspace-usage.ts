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
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import { getUserQuotaDates } from '../api'

/** The overview summary and trend share the same hourly usage snapshot. */
export function useWorkspaceUsage(days: 7 | 30 = 7) {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const today = new Date().setHours(0, 0, 0, 0)
  const period = useMemo(() => {
    const start = new Date(today)
    start.setDate(start.getDate() - days + 1)
    const end = new Date(today)
    end.setDate(end.getDate() + 1)
    return {
      start_timestamp: start.getTime() / 1000,
      end_timestamp: end.getTime() / 1000 - 1,
    }
  }, [days, today])
  const query = useQuery({
    queryKey: ['dashboard', 'workspace-usage', userId, period],
    enabled: Boolean(userId),
    queryFn: async () => {
      // The API caps elapsed time at 30 * 24 hours. DST can make 30 local
      // calendar days longer; fetch the remainder without overlapping rows.
      const firstEnd = Math.min(
        period.end_timestamp,
        period.start_timestamp + 30 * 86400
      )
      const ranges = [
        { start_timestamp: period.start_timestamp, end_timestamp: firstEnd },
      ]
      if (firstEnd < period.end_timestamp) {
        ranges.push({
          start_timestamp: firstEnd + 1,
          end_timestamp: period.end_timestamp,
        })
      }
      const responses = await Promise.all(
        ranges.map((range) => getUserQuotaDates(range))
      )
      if (responses.some((response) => !response.success)) {
        throw new Error('Usage request failed')
      }
      return responses.flatMap((response) => response.data ?? [])
    },
    staleTime: 60_000,
  })
  const buckets = useMemo(() => {
    const daily = new Map<
      number,
      { timestamp: number; quota: number; count: number; tokens: number }
    >()
    for (let index = 0; index < days; index++) {
      const date = new Date(period.start_timestamp * 1000)
      date.setDate(date.getDate() + index)
      const timestamp = date.getTime() / 1000
      daily.set(timestamp, { timestamp, quota: 0, count: 0, tokens: 0 })
    }
    // The API returns hourly rows; default_time does not aggregate them.
    for (const item of query.data ?? []) {
      const timestamp =
        new Date(item.created_at * 1000).setHours(0, 0, 0, 0) / 1000
      const bucket = daily.get(timestamp)
      if (!bucket) continue
      bucket.quota += item.quota ?? 0
      bucket.count += item.count ?? 0
      bucket.tokens += item.token_used ?? 0
    }
    return [...daily.values()]
  }, [days, period, query.data])
  return { query, buckets, period }
}
