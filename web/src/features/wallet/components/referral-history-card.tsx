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
import { formatQuota } from '@/lib/format'

import { getReferralRewards } from '../api'
import type { AffiliateRewardItem } from '../types'

export function ReferralHistoryCard() {
  const { t } = useTranslation()
  const [rewards, setRewards] = useState<AffiliateRewardItem[]>([])

  const fetchRewards = useCallback(async () => {
    try {
      const response = await getReferralRewards(1, 20)
      if (response.success && response.data) setRewards(response.data.items)
    } catch {
      setRewards([])
    }
  }, [])

  useEffect(() => {
    fetchRewards()
  }, [fetchRewards])

  return (
    <Card data-card-hover='false' className='bg-muted/20 py-0'>
      <CardHeader>
        <CardTitle>{t('Referral History')}</CardTitle>
      </CardHeader>
      <CardContent className='p-0 sm:p-0'>
        {rewards.length === 0 ? (
          <Empty className='p-8'>
            <EmptyTitle>{t('No rewards yet')}</EmptyTitle>
            <EmptyDescription>
              {t('Referral bonus records will appear here')}
            </EmptyDescription>
          </Empty>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Created')}</TableHead>
                <TableHead>{t('Status')}</TableHead>
                <TableHead className='text-right'>
                  {t('Eligible Paid Usage')}
                </TableHead>
                <TableHead className='text-right'>{t('Bonus')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rewards.map((reward) => (
                <TableRow key={reward.id}>
                  <TableCell className='text-xs'>
                    {new Date(reward.created_at * 1000).toLocaleString()}
                  </TableCell>
                  <TableCell>{t(reward.status)}</TableCell>
                  <TableCell className='text-right font-mono'>
                    {formatQuota(reward.eligible_paid_quota)}
                  </TableCell>
                  <TableCell className='text-right font-mono'>
                    {formatQuota(reward.reward_quota)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}
