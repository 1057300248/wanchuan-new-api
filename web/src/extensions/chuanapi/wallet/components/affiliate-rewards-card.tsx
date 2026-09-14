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
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota } from '@/lib/format'

import { REFERRAL_REWARD_MAX_TOPUP_EVENTS } from '../constants'
import { formatReferralRate } from '../lib'
import type { ReferralCommissionItem, UserWalletData } from '../types'
import { ReferralCommissionRecords } from './referral-commission-records'

interface AffiliateRewardsCardProps {
  user: UserWalletData | null
  affiliateLink: string
  onTransfer: () => void
  complianceConfirmed?: boolean
  referralRate: number
  records: ReferralCommissionItem[]
  recordsTotal: number
  recordsLoading?: boolean
  recordsError?: unknown
  onRecordsRetry?: () => void
  recordsPage: number
  onRecordsPageChange: (page: number) => void
  loading?: boolean
}

export function AffiliateRewardsCard(props: AffiliateRewardsCardProps) {
  const { t } = useTranslation()

  if (props.loading) {
    return (
      <Card data-card-hover='false' className='bg-muted/20 py-0'>
        <CardContent className='grid gap-4 p-3 sm:p-4 lg:grid-cols-[minmax(220px,1fr)_minmax(220px,0.72fr)_minmax(320px,1.15fr)] lg:items-center'>
          <div>
            <Skeleton className='h-5 w-32' />
            <Skeleton className='mt-2 h-4 w-48' />
          </div>
          <Skeleton className='h-14 rounded-lg' />
          <Skeleton className='h-10 rounded-lg' />
          <Skeleton className='h-24 rounded-lg lg:col-span-3' />
        </CardContent>
      </Card>
    )
  }

  const hasRewards = (props.user?.aff_quota ?? 0) > 0
  const stats = [
    [t('Pending Rewards'), formatQuota(props.user?.aff_quota ?? 0)],
    [t('Total Rewards'), formatQuota(props.user?.aff_history_quota ?? 0)],
    [t('Referral Invited Users'), String(props.user?.aff_count ?? 0)],
  ]

  return (
    <Card data-card-hover='false' className='bg-muted/20 py-0'>
      <CardContent className='grid gap-3 p-3 sm:gap-4 sm:p-4 lg:grid-cols-[minmax(200px,1fr)_minmax(180px,0.65fr)_minmax(280px,1fr)] lg:items-center'>
        <div className='flex min-w-0 items-center gap-2.5'>
          <IconBadge tone='chart-3'>
            <Share2 />
          </IconBadge>
          <div className='min-w-0'>
            <h3 className='truncate text-sm font-semibold'>
              {t('Invitation Rewards')}
            </h3>
            <p className='text-muted-foreground line-clamp-2 text-xs'>
              {t('Invite friends to earn extra rewards')}
            </p>
          </div>
        </div>

        <div className='grid grid-cols-3 gap-1.5 text-center'>
          {stats.map(([label, value]) => (
            <div key={label}>
              <div className='text-muted-foreground truncate text-[10px] font-medium tracking-wider uppercase'>
                {label}
              </div>
              <div className='mt-0.5 truncate text-sm font-semibold tabular-nums'>
                {value}
              </div>
            </div>
          ))}
        </div>

        <div className='flex items-center gap-2'>
          <Input
            value={props.affiliateLink}
            readOnly
            aria-label={t('Invitation Link')}
            className='border-muted bg-background/70 h-9 min-w-0 flex-1 font-mono text-xs'
          />
          <CopyButton
            value={props.affiliateLink}
            variant='outline'
            size='sm'
            className='bg-background h-9 shrink-0 px-2.5'
            iconClassName='size-4'
            tooltip={t('Copy referral link')}
            aria-label={t('Copy referral link')}
          >
            {t('Copy')}
          </CopyButton>
          {hasRewards && (
            <Button
              onClick={props.onTransfer}
              disabled={!props.complianceConfirmed}
              className='h-9 shrink-0 px-3'
              size='sm'
            >
              {t('Referral Transfer to Balance')}
            </Button>
          )}
        </div>
        {!props.complianceConfirmed ? (
          <p className='text-muted-foreground text-xs lg:col-span-3'>
            {t(
              'Referral reward transfer is disabled until the administrator confirms compliance terms.'
            )}
          </p>
        ) : null}

        <div className='space-y-1.5 lg:col-span-3'>
          <h4 className='text-sm font-semibold'>{t('Reward Rules')}</h4>
          <ol className='text-muted-foreground list-decimal space-y-1 ps-4 text-xs'>
            <li>
              {t(
                'Invite friends to register. You will receive rewards after they recharge.'
              )}
            </li>
            <li>
              {t(
                'For the first {{limit}} recharges of an invited friend, you will receive {{rate}} of the recharge amount as a reward.',
                {
                  limit: REFERRAL_REWARD_MAX_TOPUP_EVENTS,
                  rate: formatReferralRate(props.referralRate),
                }
              )}
            </li>
            <li>
              {t(
                'Use the transfer feature to move reward credits into your account balance.'
              )}
            </li>
            <li>
              {t('The more friends you invite, the more rewards you earn.')}
            </li>
          </ol>
        </div>

        <div className='space-y-2 lg:col-span-3'>
          <h4 className='text-sm font-semibold'>{t('Commission Records')}</h4>
          <ReferralCommissionRecords
            records={props.records}
            total={props.recordsTotal}
            loading={props.recordsLoading}
            error={props.recordsError}
            onRetry={props.onRecordsRetry}
            page={props.recordsPage}
            onPageChange={props.onRecordsPageChange}
          />
        </div>
      </CardContent>
    </Card>
  )
}
