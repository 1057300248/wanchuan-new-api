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

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Empty, EmptyDescription, EmptyTitle } from '@/components/ui/empty'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { formatQuota } from '@/lib/format'

import { getReferralRewards, getReferralWithdrawals } from '../api'
import { withdrawalStatusLabelKey } from '../lib/ledger'
import type { AffiliateRewardItem, WithdrawalItem } from '../types'

function formatTime(ts: number): string {
  if (!ts) return '—'
  return new Date(ts * 1000).toLocaleString()
}

export function ReferralHistoryCard() {
  const { t } = useTranslation()
  const [rewards, setRewards] = useState<AffiliateRewardItem[]>([])
  const [withdrawals, setWithdrawals] = useState<WithdrawalItem[]>([])

  const fetchData = useCallback(async () => {
    try {
      const [rewardsRes, withdrawalsRes] = await Promise.all([
        getReferralRewards(1, 10),
        getReferralWithdrawals(1, 10),
      ])
      if (rewardsRes.success && rewardsRes.data) {
        setRewards(rewardsRes.data.items)
      }
      if (withdrawalsRes.success && withdrawalsRes.data) {
        setWithdrawals(withdrawalsRes.data.items)
      }
    } catch {
      // keep previous data
    }
  }, [])

  useEffect(() => {
    fetchData()
  }, [fetchData])

  return (
    <Card data-card-hover='false' className='bg-muted/20 py-0'>
      <CardHeader>
        <CardTitle>{t('Referral History')}</CardTitle>
      </CardHeader>
      <CardContent className='p-0 sm:p-0'>
        <Tabs defaultValue='rewards' className='p-3 sm:p-4'>
          <TabsList className='mb-3'>
            <TabsTrigger value='rewards'>{t('Rewards')}</TabsTrigger>
            <TabsTrigger value='withdrawals'>{t('Withdrawals')}</TabsTrigger>
          </TabsList>
          <TabsContent value='rewards'>
            {rewards.length === 0 ? (
              <Empty className='p-8'>
                <EmptyTitle>{t('No rewards yet')}</EmptyTitle>
                <EmptyDescription>
                  {t('Referral rewards will appear here')}
                </EmptyDescription>
              </Empty>
            ) : (
              <div className='overflow-x-auto'>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('Time')}</TableHead>
                      <TableHead className='text-right'>
                        {t('Eligible Paid')}
                      </TableHead>
                      <TableHead className='text-right'>
                        {t('Reward')}
                      </TableHead>
                      <TableHead>{t('Status')}</TableHead>
                      <TableHead>{t('Unlocks At')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {rewards.map((reward) => (
                      <TableRow key={reward.id}>
                        <TableCell className='text-muted-foreground text-xs whitespace-nowrap'>
                          {formatTime(reward.created_at)}
                        </TableCell>
                        <TableCell className='text-right font-mono text-xs'>
                          {formatQuota(reward.eligible_paid_quota)}
                        </TableCell>
                        <TableCell className='text-right font-mono text-xs font-semibold'>
                          +{formatQuota(reward.reward_quota)}
                        </TableCell>
                        <TableCell className='text-xs'>
                          {t(
                            reward.status === 'pending'
                              ? 'Pending'
                              : 'Available'
                          )}
                        </TableCell>
                        <TableCell className='text-muted-foreground text-xs whitespace-nowrap'>
                          {reward.status === 'pending'
                            ? formatTime(reward.unlock_at)
                            : '—'}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </TabsContent>
          <TabsContent value='withdrawals'>
            {withdrawals.length === 0 ? (
              <Empty className='p-8'>
                <EmptyTitle>{t('No withdrawal requests yet')}</EmptyTitle>
                <EmptyDescription>
                  {t('Withdrawal requests will appear here')}
                </EmptyDescription>
              </Empty>
            ) : (
              <div className='overflow-x-auto'>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('Time')}</TableHead>
                      <TableHead className='text-right'>
                        {t('Amount')}
                      </TableHead>
                      <TableHead>{t('Status')}</TableHead>
                      <TableHead>{t('Note')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {withdrawals.map((item) => (
                      <TableRow key={item.id}>
                        <TableCell className='text-muted-foreground text-xs whitespace-nowrap'>
                          {formatTime(item.created_at)}
                        </TableCell>
                        <TableCell className='text-right font-mono text-xs font-semibold'>
                          -{formatQuota(item.amount)}
                        </TableCell>
                        <TableCell className='text-xs'>
                          {t(withdrawalStatusLabelKey(item.status))}
                        </TableCell>
                        <TableCell className='text-muted-foreground max-w-40 truncate text-xs'>
                          {item.reject_reason || '—'}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </TabsContent>
        </Tabs>
      </CardContent>
    </Card>
  )
}
