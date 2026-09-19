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
import { ListChecks, RefreshCw } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { listSystemTasks } from '@/features/system-settings/api'
import { createServerError } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import { SystemTaskHistory } from './system-task-history'
import { SystemTasksTable } from './system-tasks-table'

const ACTIVE_POLL_INTERVAL_MS = 8000

const SYSTEM_TASKS_SKELETON_KEYS = [
  'system-tasks-skeleton-1',
  'system-tasks-skeleton-2',
  'system-tasks-skeleton-3',
  'system-tasks-skeleton-4',
]

export function SystemTasksPanel() {
  const { t } = useTranslation()
  const tasksQuery = useQuery({
    queryKey: ['system-info', 'system-tasks', 'active'],
    queryFn: async () => {
      const res = await listSystemTasks(100, { scope: 'active' })
      if (!res.success || !Array.isArray(res.data)) {
        throw createServerError(res, t('We could not load system tasks.'))
      }
      return res.data
    },
    staleTime: 30 * 1000,
    retry: false,
    refetchInterval: (query) =>
      query.state.data?.length ? ACTIVE_POLL_INTERVAL_MS : false,
  })

  const activeTasks = tasksQuery.data ?? []
  const loading = tasksQuery.isLoading
  const refreshing = tasksQuery.isFetching && !loading
  const hasActiveTasks = activeTasks.length > 0

  let tasksBody: ReactNode
  if (loading) {
    tasksBody = (
      <div className='space-y-2 p-4 sm:p-5'>
        {SYSTEM_TASKS_SKELETON_KEYS.map((skeletonKey) => (
          <Skeleton key={skeletonKey} className='h-9 w-full rounded-md' />
        ))}
      </div>
    )
  } else if (tasksQuery.isError) {
    const errorMessage =
      tasksQuery.error instanceof Error ? tasksQuery.error.message : undefined
    tasksBody = (
      <ErrorState
        title={t('We could not load system tasks.')}
        description={errorMessage}
        onRetry={() => {
          void tasksQuery.refetch()
        }}
        className='min-h-[260px]'
      />
    )
  } else if (activeTasks.length === 0) {
    tasksBody = (
      <div className='px-4 py-10 text-center sm:px-5'>
        <div className='bg-muted mx-auto mb-3 flex size-10 items-center justify-center rounded-lg'>
          <ListChecks
            className='text-muted-foreground size-5'
            aria-hidden='true'
          />
        </div>
        <p className='text-muted-foreground text-sm'>
          {t('No active system tasks.')}
        </p>
      </div>
    )
  } else {
    tasksBody = null
  }

  return (
    <section className='bg-card overflow-hidden rounded-lg border shadow-xs'>
      <div className='flex flex-col gap-3 border-b px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5'>
        <div className='min-w-0'>
          <div className='flex items-center gap-2'>
            <span className='bg-muted text-muted-foreground inline-flex size-7 items-center justify-center rounded-md'>
              <ListChecks className='size-4' aria-hidden='true' />
            </span>
            <div className='min-w-0'>
              <h3 className='text-sm font-semibold'>{t('System Tasks')}</h3>
              <p className='text-muted-foreground mt-0.5 text-xs'>
                {t(
                  'Recent maintenance tasks running across instances and their execution status.'
                )}
              </p>
            </div>
          </div>
        </div>
        <div className='flex shrink-0 items-center gap-3'>
          <span
            className='text-muted-foreground inline-flex items-center gap-1.5 text-xs'
            aria-live='polite'
          >
            <span
              className={cn(
                'size-1.5 rounded-full',
                hasActiveTasks ? 'bg-emerald-500' : 'bg-muted-foreground/40'
              )}
              aria-hidden='true'
            />
            {hasActiveTasks
              ? t('Auto-refreshing every {{seconds}}s', {
                  seconds: ACTIVE_POLL_INTERVAL_MS / 1000,
                })
              : t('Live refresh pauses when no task is running')}
          </span>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void tasksQuery.refetch()}
            disabled={tasksQuery.isFetching}
            aria-label={t('Refresh')}
          >
            <RefreshCw
              data-icon='inline-start'
              className={cn('size-3.5', refreshing && 'animate-spin')}
              aria-hidden='true'
            />
            {refreshing ? t('Refreshing...') : t('Refresh')}
          </Button>
        </div>
      </div>

      <div aria-busy={tasksQuery.isFetching}>
        {tasksBody ?? (
          <div className='space-y-4 p-4 sm:p-5'>
            <div>
              <div className='mb-2 flex items-center justify-between gap-3'>
                <div>
                  <h4 className='text-sm font-medium'>{t('Active Tasks')}</h4>
                  <p className='text-muted-foreground mt-0.5 text-xs'>
                    {t('Tasks currently pending or running.')}
                  </p>
                </div>
                <Badge variant='outline'>{activeTasks.length}</Badge>
              </div>
              {activeTasks.length > 0 ? (
                <SystemTasksTable tasks={activeTasks} />
              ) : (
                <div className='text-muted-foreground rounded-md border border-dashed px-4 py-6 text-center text-sm'>
                  {t('No active system tasks.')}
                </div>
              )}
            </div>
          </div>
        )}
      </div>
      <div className='p-4 pt-0 sm:p-5 sm:pt-0'>
        <SystemTaskHistory activeRefreshAt={tasksQuery.dataUpdatedAt} />
      </div>
    </section>
  )
}
