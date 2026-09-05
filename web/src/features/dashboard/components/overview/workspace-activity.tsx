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
import { Link } from '@tanstack/react-router'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { getUserLogs } from '@/features/usage-logs/api'
import { LOG_TYPE_ENUM } from '@/features/usage-logs/constants'
import { usageLogSchema } from '@/features/usage-logs/data/schema'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { useWorkspaceUsage } from '../../hooks/use-workspace-usage'

export function WorkspaceActivity(props: { quickstart?: ReactNode }) {
  const { t, i18n } = useTranslation()
  const dateLocale = toIntlLocale(i18n.resolvedLanguage ?? i18n.language)
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [days, setDays] = useState<7 | 30>(7)
  const [metric, setMetric] = useState<'quota' | 'count'>('quota')
  const { query: usage, buckets } = useWorkspaceUsage(days)
  const logs = useQuery({
    queryKey: ['workspace', userId, 'recent-logs'],
    enabled: Boolean(userId),
    queryFn: async () => {
      const results = await Promise.all([
        getUserLogs({ p: 1, page_size: 5, type: LOG_TYPE_ENUM.CONSUME }),
        getUserLogs({ p: 1, page_size: 5, type: LOG_TYPE_ENUM.ERROR }),
      ])
      if (results.some((result) => !result.success)) {
        throw new Error('Logs request failed')
      }
      return results
        .flatMap((result) =>
          usageLogSchema.array().parse(result.data?.items ?? [])
        )
        .filter(
          (log) =>
            log.type === LOG_TYPE_ENUM.CONSUME ||
            log.type === LOG_TYPE_ENUM.ERROR
        )
        .sort(
          (left, right) =>
            right.created_at - left.created_at || right.id - left.id
        )
        .filter(
          (log, index, all) =>
            all.findIndex((item) => item.id === log.id) === index
        )
        .slice(0, 5)
    },
    staleTime: 60_000,
  })
  const points = buckets.map(
    (bucket) => [bucket.timestamp, bucket[metric]] as const
  )
  const hasUsage = (usage.data?.length ?? 0) > 0
  const maximum = Math.max(1, ...points.map(([, value]) => value))

  return (
    <section
      className={cn(
        'grid min-w-0 gap-6',
        props.quickstart
          ? 'xl:grid-cols-[minmax(0,1fr)_22rem]'
          : 'xl:grid-cols-2'
      )}
      aria-label={t('Usage')}
    >
      <div className='bg-card min-h-72 min-w-0 rounded-lg border p-4 sm:p-6'>
        <div className='mb-4 flex flex-wrap items-center justify-between gap-3'>
          <h3 className='font-semibold'>{t('Usage')}</h3>
          <div className='flex flex-wrap gap-1'>
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
          <Button
            size='sm'
            variant={metric === 'quota' ? 'secondary' : 'ghost'}
            aria-pressed={metric === 'quota'}
            onClick={() => setMetric('quota')}
          >
            {t('Consumption')}
          </Button>
          <Button
            size='sm'
            variant={metric === 'count' ? 'secondary' : 'ghost'}
            aria-pressed={metric === 'count'}
            onClick={() => setMetric('count')}
          >
            {t('Requests')}
          </Button>
        </div>
        {usage.isPending && <p role='status'>{t('Loading')}</p>}
        {usage.isError && (
          <div role='alert'>
            {t('Failed to load data')}{' '}
            <Button variant='outline' onClick={() => void usage.refetch()}>
              {t('Retry')}
            </Button>
          </div>
        )}
        {usage.isSuccess && !hasUsage && (
          <p className='text-muted-foreground py-12'>{t('No recent usage')}</p>
        )}
        {usage.isSuccess && hasUsage && (
          <>
            <svg
              viewBox='0 0 400 120'
              className='text-foreground h-40 w-full'
              preserveAspectRatio='none'
              role='img'
              aria-label={metric === 'quota' ? t('Consumption') : t('Requests')}
            >
              <polyline
                fill='none'
                stroke='currentColor'
                strokeWidth='2'
                points={points
                  .map(
                    ([, value], index) =>
                      `${10 + (index * 380) / Math.max(1, points.length - 1)},${110 - (value / maximum) * 100}`
                  )
                  .join(' ')}
              />
              {points.map(([timestamp, value], index) => (
                <circle
                  key={timestamp}
                  cx={10 + (index * 380) / Math.max(1, points.length - 1)}
                  cy={110 - (value / maximum) * 100}
                  r='3'
                  fill='currentColor'
                />
              ))}
            </svg>
            <div className='text-muted-foreground flex justify-between gap-2 text-xs'>
              <time>
                {new Date(points[0][0] * 1000).toLocaleDateString(dateLocale)}
              </time>
              <time>
                {new Date(
                  (points.at(-1)?.[0] ?? points[0][0]) * 1000
                ).toLocaleDateString(dateLocale)}
              </time>
            </div>
            <details className='mt-3 text-sm'>
              <summary className='cursor-pointer'>{t('Details')}</summary>
              <div className='max-h-40 overflow-auto'>
                {points.map(([timestamp, value]) => (
                  <div
                    key={timestamp}
                    className='flex justify-between gap-3 py-1'
                  >
                    <time>
                      {new Date(timestamp * 1000).toLocaleDateString(
                        dateLocale
                      )}
                    </time>
                    <span>
                      {metric === 'quota'
                        ? formatQuota(value)
                        : formatNumber(value)}
                    </span>
                  </div>
                ))}
              </div>
            </details>
          </>
        )}
      </div>
      {props.quickstart && <div className='min-w-0'>{props.quickstart}</div>}
      <div
        className={cn(
          'min-w-0 border-t pt-5',
          props.quickstart && 'xl:col-span-2'
        )}
      >
        <div className='mb-4 flex flex-wrap items-center justify-between gap-3'>
          <h3 className='font-semibold'>{t('Usage Logs')}</h3>
          <Button variant='ghost' size='sm' render={<Link to='/usage-logs' />}>
            {t('View all')}
          </Button>
        </div>
        {logs.isPending && <p role='status'>{t('Loading')}</p>}
        {logs.isError && (
          <div role='alert'>
            {t('Failed to load data')}{' '}
            <Button variant='outline' onClick={() => void logs.refetch()}>
              {t('Retry')}
            </Button>
          </div>
        )}
        {logs.isSuccess && logs.data.length === 0 && (
          <p className='text-muted-foreground py-12'>{t('No recent usage')}</p>
        )}
        {logs.isSuccess && logs.data.length > 0 && (
          <div className='overflow-x-auto'>
            <table className='w-full text-left text-sm'>
              <thead className='text-muted-foreground'>
                <tr>
                  <th scope='col' className='py-2'>
                    {t('Model')}
                  </th>
                  <th scope='col'>{t('Status')}</th>
                  <th scope='col'>{t('Duration')}</th>
                  <th scope='col'>{t('Consumption')}</th>
                </tr>
              </thead>
              <tbody>
                {logs.data.map((log) => (
                  <tr key={log.id} className='border-t'>
                    <td className='max-w-48 py-3 pr-3'>
                      <span
                        className='block truncate font-mono text-xs'
                        title={log.model_name}
                      >
                        {log.model_name}
                      </span>
                      <time className='text-muted-foreground text-xs'>
                        {new Date(log.created_at * 1000).toLocaleString(
                          dateLocale
                        )}
                      </time>
                    </td>
                    <td className='pr-3 whitespace-nowrap'>
                      {log.type === LOG_TYPE_ENUM.ERROR
                        ? t('Error')
                        : t('Success')}
                    </td>
                    <td className='pr-3 whitespace-nowrap'>{log.use_time}s</td>
                    <td className='whitespace-nowrap'>
                      {formatQuota(log.quota)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  )
}
