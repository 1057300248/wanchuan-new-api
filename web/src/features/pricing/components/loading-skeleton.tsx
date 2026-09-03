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
import { Skeleton } from '@/components/ui/skeleton'

import { VIEW_MODES, type ViewMode } from '../constants'

const CARD_SKELETON_KEYS = [
  'pricing-card-skeleton-1',
  'pricing-card-skeleton-2',
  'pricing-card-skeleton-3',
  'pricing-card-skeleton-4',
  'pricing-card-skeleton-5',
  'pricing-card-skeleton-6',
  'pricing-card-skeleton-7',
  'pricing-card-skeleton-8',
  'pricing-card-skeleton-9',
]

const FILTER_BAR_SKELETON_WIDTHS = [80, 90, 75, 85, 70]

const TABLE_COLUMNS = [
  { key: 'pricing-table-col-200', width: 200 },
  { key: 'pricing-table-col-100-a', width: 100 },
  { key: 'pricing-table-col-100-b', width: 100 },
  { key: 'pricing-table-col-100-c', width: 100 },
  { key: 'pricing-table-col-80', width: 80 },
  { key: 'pricing-table-col-100-d', width: 100 },
]

const TABLE_ROW_KEYS = [
  'pricing-table-row-1',
  'pricing-table-row-2',
  'pricing-table-row-3',
  'pricing-table-row-4',
  'pricing-table-row-5',
  'pricing-table-row-6',
  'pricing-table-row-7',
  'pricing-table-row-8',
  'pricing-table-row-9',
  'pricing-table-row-10',
]

const TABLE_FOOTER_KEYS = [
  'pricing-table-footer-1',
  'pricing-table-footer-2',
  'pricing-table-footer-3',
  'pricing-table-footer-4',
]

export interface LoadingSkeletonProps {
  viewMode?: ViewMode
}

export function LoadingSkeleton(props: LoadingSkeletonProps) {
  const viewMode = props.viewMode ?? VIEW_MODES.CARD

  return (
    <div className='space-y-5'>
      <div className='space-y-1.5'>
        <Skeleton className='h-8 w-40' />
        <Skeleton className='h-4 w-52' />
      </div>
      <Skeleton className='h-10 w-full rounded-lg' />
      <FilterBarSkeleton />
      {viewMode === VIEW_MODES.TABLE ? (
        <TableContentSkeleton />
      ) : (
        <CardContentSkeleton />
      )}
    </div>
  )
}

function CardContentSkeleton() {
  return (
    <div className='grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3'>
      {CARD_SKELETON_KEYS.map((skeletonKey) => (
        <div key={skeletonKey} className='rounded-xl border p-5'>
          <div className='flex items-start justify-between gap-3'>
            <div className='flex min-w-0 items-start gap-3'>
              <Skeleton className='size-10 shrink-0 rounded-xl' />
              <div className='min-w-0 flex-1 space-y-2'>
                <Skeleton className='h-5 w-36' />
                <Skeleton className='h-3.5 w-48' />
              </div>
            </div>
            <Skeleton className='h-8 w-16 rounded-md' />
          </div>
          <div className='mt-4 space-y-2'>
            <Skeleton className='h-3.5 w-full' />
            <Skeleton className='h-3.5 w-4/5' />
          </div>
          <div className='mt-4 flex items-center gap-2'>
            <Skeleton className='h-4 w-24' />
            <Skeleton className='h-4 w-16' />
          </div>
          <div className='mt-2 flex items-center gap-3'>
            <Skeleton className='h-3.5 w-14' />
            <Skeleton className='h-3.5 w-14' />
            <Skeleton className='h-3.5 w-8' />
          </div>
        </div>
      ))}
    </div>
  )
}

function FilterBarSkeleton() {
  return (
    <div className='space-y-3'>
      <div className='flex items-center gap-3'>
        <div className='flex flex-1 flex-wrap items-center gap-2'>
          {FILTER_BAR_SKELETON_WIDTHS.map((width) => (
            <Skeleton
              key={`pricing-filter-${width}`}
              className='h-8 rounded-lg'
              style={{ width: `${width}px` }}
            />
          ))}
        </div>
        <div className='flex items-center gap-2'>
          <Skeleton className='h-8 w-24 rounded-lg' />
          <Skeleton className='h-8 w-20 rounded-lg' />
          <Skeleton className='h-8 w-24' />
          <Skeleton className='h-8 w-20 rounded-lg' />
        </div>
      </div>
      <Skeleton className='h-5 w-24' />
    </div>
  )
}

function TableContentSkeleton() {
  const columns = TABLE_COLUMNS

  return (
    <div className='space-y-4'>
      <div className='overflow-hidden rounded-lg border'>
        <div className='bg-muted/30 border-b px-4 py-3'>
          <div className='flex items-center gap-4'>
            {columns.map((col) => (
              <Skeleton
                key={col.key}
                className='h-4'
                style={{ width: `${col.width}px` }}
              />
            ))}
          </div>
        </div>
        {TABLE_ROW_KEYS.map((rowKey) => (
          <div
            key={rowKey}
            className='flex items-center gap-4 border-b px-4 py-3 last:border-b-0'
          >
            {columns.map((col) => (
              <Skeleton
                key={`${rowKey}-${col.key}`}
                className='h-5'
                style={{ width: `${col.width}px` }}
              />
            ))}
          </div>
        ))}
      </div>
      <div className='flex items-center justify-between'>
        <Skeleton className='h-5 w-32' />
        <div className='flex items-center gap-2'>
          {TABLE_FOOTER_KEYS.map((footerKey) => (
            <Skeleton key={footerKey} className='size-8' />
          ))}
        </div>
      </div>
    </div>
  )
}
