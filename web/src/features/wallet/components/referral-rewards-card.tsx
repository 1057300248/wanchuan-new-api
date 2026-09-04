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
import { Share2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota } from '@/lib/format'

import type { ReferralOverview } from '../types'

interface ReferralRewardsCardProps {
  overview: ReferralOverview | null
  referralLink: string
  loading?: boolean
}

export function ReferralRewardsCard(props: ReferralRewardsCardProps) {
  const { t } = useTranslation()
  if (props.loading) {
    return <Skeleton className='h-40 w-full' />
  }

  const rate = Math.round((props.overview?.reward_rate ?? 0.2) * 100)
  const days = props.overview?.freeze_days ?? 7
  const values = [
    [t('Pending'), props.overview?.pending_bonus_quota ?? 0],
    [t('Credited'), props.overview?.credited_bonus_quota ?? 0],
    [t('Total Rewards'), props.overview?.total_bonus_quota ?? 0],
  ] as const

  return (
    <Card data-card-hover='false' className='bg-muted/20 py-0'>
      <CardHeader>
        <CardTitle className='flex items-center gap-2'>
          <Share2 className='size-4' />
          {t('Referral Bonus')}
        </CardTitle>
      </CardHeader>
      <CardContent className='space-y-4 pb-4'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Earn {{rate}}% of eligible paid consumption. Rewards unlock to bonus balance after {{days}} days.',
            { rate, days }
          )}
        </p>
        <div className='grid grid-cols-3 gap-2'>
          {values.map(([label, value]) => (
            <div key={label} className='rounded-md border p-3 text-center'>
              <div className='text-muted-foreground text-xs'>{label}</div>
              <div className='mt-1 font-mono font-semibold tabular-nums'>
                {formatQuota(value)}
              </div>
            </div>
          ))}
        </div>
        <div className='flex items-center gap-2'>
          <Input
            value={props.referralLink}
            readOnly
            aria-label={t('Referral link')}
          />
          <CopyButton
            value={props.referralLink}
            variant='outline'
            tooltip={t('Copy referral link')}
            aria-label={t('Copy referral link')}
          />
        </div>
      </CardContent>
    </Card>
  )
}
