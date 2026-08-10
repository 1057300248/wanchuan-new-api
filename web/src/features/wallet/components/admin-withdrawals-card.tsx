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
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { NativeSelect } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatQuota } from '@/lib/format'

import {
  approveAdminReferralWithdrawal,
  getAdminReferralWithdrawals,
  payAdminReferralWithdrawal,
  rejectAdminReferralWithdrawal,
} from '../api'
import type { WithdrawalItem, WithdrawalStatus } from '../types'

const PAGE_SIZE = 20

function statusVariant(status: WithdrawalStatus) {
  if (status === 'paid') return 'default' as const
  if (status === 'rejected') return 'destructive' as const
  if (status === 'approved') return 'warning' as const
  return 'secondary' as const
}

function statusLabel(status: WithdrawalStatus) {
  if (status === 'pending') return 'Pending Review'
  if (status === 'approved') return 'Approved'
  if (status === 'rejected') return 'Rejected'
  return 'Paid'
}

export function AdminWithdrawalsCard() {
  const { t } = useTranslation()
  const [items, setItems] = useState<WithdrawalItem[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('')
  const [loading, setLoading] = useState(true)
  const [actingId, setActingId] = useState<number | null>(null)

  const fetchRows = useCallback(async () => {
    try {
      setLoading(true)
      const response = await getAdminReferralWithdrawals(
        page,
        PAGE_SIZE,
        status || undefined
      )
      if (response.success && response.data) {
        setItems(response.data.items)
        setTotal(response.data.total)
      } else {
        toast.error(response.message || t('Failed to load withdrawal requests'))
      }
    } catch {
      toast.error(t('Failed to load withdrawal requests'))
    } finally {
      setLoading(false)
    }
  }, [page, status, t])

  useEffect(() => {
    void fetchRows()
  }, [fetchRows])

  const runAction = useCallback(
    async (id: number, action: 'approve' | 'reject' | 'pay') => {
      let reason = ''
      if (action === 'reject') {
        reason = window.prompt(t('Enter rejection reason'))?.trim() ?? ''
        if (!reason) return
      } else if (!window.confirm(t('Confirm this withdrawal action?'))) {
        return
      }
      try {
        setActingId(id)
        let response
        if (action === 'approve') {
          response = await approveAdminReferralWithdrawal(id)
        } else if (action === 'reject') {
          response = await rejectAdminReferralWithdrawal(id, reason)
        } else {
          response = await payAdminReferralWithdrawal(id)
        }
        if (!response.success) {
          toast.error(response.message || t('Withdrawal action failed'))
          return
        }
        toast.success(t('Withdrawal request updated'))
        await fetchRows()
      } catch {
        toast.error(t('Withdrawal action failed'))
      } finally {
        setActingId(null)
      }
    },
    [fetchRows, t]
  )

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <Card data-card-hover='false' className='bg-muted/20 py-0'>
      <CardHeader className='flex flex-row items-center justify-between gap-3'>
        <div>
          <CardTitle>{t('Withdrawal Review')}</CardTitle>
          <p className='text-muted-foreground mt-1 text-xs'>
            {t('Approve, reject, and mark manually paid referral withdrawals')}
          </p>
        </div>
        <NativeSelect
          value={status}
          onChange={(event) => {
            setStatus(event.target.value)
            setPage(1)
          }}
          className='w-36'
        >
          <option value=''>{t('All Statuses')}</option>
          <option value='pending'>{t('Pending Review')}</option>
          <option value='approved'>{t('Approved')}</option>
          <option value='rejected'>{t('Rejected')}</option>
          <option value='paid'>{t('Paid')}</option>
        </NativeSelect>
      </CardHeader>
      <CardContent className='p-0 sm:p-0'>
        {renderBody()}
        <div className='flex items-center justify-between border-t px-4 py-3'>
          <span className='text-muted-foreground text-xs'>
            {t('Total')}: {total}
          </span>
          <div className='flex gap-2'>
            <Button
              size='sm'
              variant='outline'
              disabled={page <= 1 || loading}
              onClick={() => setPage((value) => Math.max(1, value - 1))}
            >
              {t('Previous')}
            </Button>
            <Button
              size='sm'
              variant='outline'
              disabled={page >= totalPages || loading}
              onClick={() =>
                setPage((value) => Math.min(totalPages, value + 1))
              }
            >
              {t('Next')}
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  )

  function renderBody() {
    if (loading) {
      return (
        <div className='space-y-2 p-4'>
          {[1, 2, 3].map((key) => (
            <Skeleton key={key} className='h-10 w-full' />
          ))}
        </div>
      )
    }
    if (items.length === 0) {
      return (
        <div className='text-muted-foreground p-8 text-center text-sm'>
          {t('No withdrawal requests yet')}
        </div>
      )
    }
    return (
      <div className='overflow-x-auto'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('User ID')}</TableHead>
              <TableHead>{t('Amount')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead>{t('Time')}</TableHead>
              <TableHead className='text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((item) => (
              <TableRow key={item.id}>
                <TableCell className='font-mono'>{item.user_id}</TableCell>
                <TableCell className='font-mono'>
                  {formatQuota(item.amount)}
                </TableCell>
                <TableCell>
                  <Badge variant={statusVariant(item.status)}>
                    {t(statusLabel(item.status))}
                  </Badge>
                </TableCell>
                <TableCell className='text-muted-foreground text-xs'>
                  {new Date(item.created_at * 1000).toLocaleString()}
                </TableCell>
                <TableCell className='text-right'>
                  <div className='flex justify-end gap-2'>
                    {item.status === 'pending' ? (
                      <>
                        <Button
                          size='sm'
                          variant='outline'
                          disabled={actingId === item.id}
                          onClick={() => void runAction(item.id, 'reject')}
                        >
                          {t('Reject')}
                        </Button>
                        <Button
                          size='sm'
                          disabled={actingId === item.id}
                          onClick={() => void runAction(item.id, 'approve')}
                        >
                          {t('Approve')}
                        </Button>
                      </>
                    ) : null}
                    {item.status === 'approved' ? (
                      <Button
                        size='sm'
                        disabled={actingId === item.id}
                        onClick={() => void runAction(item.id, 'pay')}
                      >
                        {t('Mark Paid')}
                      </Button>
                    ) : null}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    )
  }
}
