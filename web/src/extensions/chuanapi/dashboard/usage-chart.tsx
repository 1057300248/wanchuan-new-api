/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useQuery } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { api } from '@/lib/api'
import { formatNumber, formatQuota } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

type UsageRow = { created_at: number; quota?: number; count?: number }
type UsagePoint = { timestamp: number; quota: number; count: number }

function periodFor(days: 7 | 30) {
  const end = new Date()
  end.setHours(23, 59, 59, 999)
  const start = new Date(end)
  start.setDate(start.getDate() - days + 1)
  start.setHours(0, 0, 0, 0)
  return {
    start_timestamp: Math.floor(start.getTime() / 1000),
    end_timestamp: Math.floor(end.getTime() / 1000),
  }
}

export function UsageChart() {
  const { t, i18n } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [days, setDays] = useState<7 | 30>(7)
  const [metric, setMetric] = useState<'quota' | 'count'>('quota')
  const period = useMemo(() => periodFor(days), [days])
  const usage = useQuery({
    queryKey: ['extension', 'chuanapi', 'usage-chart', userId, period],
    enabled: Boolean(userId),
    queryFn: async () => {
      const firstEnd = Math.min(
        period.end_timestamp,
        period.start_timestamp + 30 * 86400
      )
      const ranges = [{ ...period, end_timestamp: firstEnd }]
      if (firstEnd < period.end_timestamp) {
        ranges.push({
          start_timestamp: firstEnd + 1,
          end_timestamp: period.end_timestamp,
        })
      }
      const responses = await Promise.all(
        ranges.map(async (range) => {
          const response = await api.get<{
            success: boolean
            data?: UsageRow[]
          }>('/api/data/self', {
            params: { ...range, default_time: 'hour' },
          })
          return requireServerSuccess(response.data).data ?? []
        })
      )
      return responses.flat()
    },
    staleTime: 60_000,
  })
  const points = useMemo(() => {
    const buckets = new Map<string, UsagePoint>()
    for (const row of usage.data ?? []) {
      const date = new Date(row.created_at * 1000)
      date.setHours(0, 0, 0, 0)
      const timestamp = date.getTime() / 1000
      const key = String(timestamp)
      const current = buckets.get(key) ?? { timestamp, quota: 0, count: 0 }
      current.quota += Number(row.quota) || 0
      current.count += Number(row.count) || 0
      buckets.set(key, current)
    }
    return [...buckets.values()].sort((a, b) => a.timestamp - b.timestamp)
  }, [usage.data])
  const values = points.map((point) => point[metric])
  const maximum = Math.max(1, ...values)
  const firstTimestamp = points[0]?.timestamp ?? 0
  const lastTimestamp = points.at(-1)?.timestamp ?? firstTimestamp
  const dateLocale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  return (
    <section
      className='bg-card min-w-0 rounded-lg border p-4 sm:p-6'
      aria-label={t('Usage')}
    >
      <div className='mb-4 flex flex-wrap items-center justify-between gap-3'>
        <h3 className='font-semibold'>{t('Usage')}</h3>
        <div className='flex gap-1'>
          {([7, 30] as const).map((value) => (
            <Button
              key={value}
              size='sm'
              variant={days === value ? 'secondary' : 'ghost'}
              aria-pressed={days === value}
              onClick={() => setDays(value)}
            >
              {value} {t('days')}
            </Button>
          ))}
        </div>
      </div>
      <div
        role='group'
        aria-label={t('Usage')}
        className='mb-4 inline-flex rounded-md border p-0.5'
      >
        {(['quota', 'count'] as const).map((value) => (
          <Button
            key={value}
            size='sm'
            variant={metric === value ? 'secondary' : 'ghost'}
            aria-pressed={metric === value}
            onClick={() => setMetric(value)}
          >
            {value === 'quota' ? t('Total Usage') : t('Requests')}
          </Button>
        ))}
      </div>
      {usage.isPending && <p role='status'>{t('Loading')}</p>}
      {usage.isError && (
        <div role='alert'>
          {t('Request failed')}{' '}
          <Button variant='outline' onClick={() => void usage.refetch()}>
            {t('Retry')}
          </Button>
        </div>
      )}
      {usage.isSuccess && points.length === 0 && (
        <p className='text-muted-foreground py-12'>{t('No recent usage')}</p>
      )}
      {usage.isSuccess && points.length > 0 && (
        <>
          <svg
            viewBox='0 0 400 120'
            className='text-foreground h-40 w-full'
            preserveAspectRatio='none'
            role='img'
            aria-label={metric === 'quota' ? t('Total Usage') : t('Requests')}
          >
            <polyline
              fill='none'
              stroke='currentColor'
              strokeWidth='2'
              points={values
                .map(
                  (value, index) =>
                    `${10 + (index * 380) / Math.max(1, values.length - 1)},${110 - (value / maximum) * 100}`
                )
                .join(' ')}
            />
            {values.map((value, index) => (
              <circle
                key={points[index].timestamp}
                cx={10 + (index * 380) / Math.max(1, values.length - 1)}
                cy={110 - (value / maximum) * 100}
                r='3'
                fill='currentColor'
              />
            ))}
          </svg>
          <div className='text-muted-foreground flex justify-between gap-2 text-xs'>
            <time>
              {new Date(firstTimestamp * 1000).toLocaleDateString(dateLocale)}
            </time>
            <time>
              {new Date(lastTimestamp * 1000).toLocaleDateString(dateLocale)}
            </time>
          </div>
          <details className='mt-3 text-sm'>
            <summary className='cursor-pointer'>{t('Details')}</summary>
            <div className='max-h-40 overflow-auto'>
              {points.map((point) => (
                <div
                  key={point.timestamp}
                  className='flex justify-between gap-3 py-1'
                >
                  <time>
                    {new Date(point.timestamp * 1000).toLocaleDateString(
                      dateLocale
                    )}
                  </time>
                  <span>
                    {metric === 'quota'
                      ? formatQuota(point.quota)
                      : formatNumber(point.count)}
                  </span>
                </div>
              ))}
            </div>
          </details>
        </>
      )}
    </section>
  )
}
