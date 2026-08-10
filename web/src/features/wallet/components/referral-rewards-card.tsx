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
import { formatQuota, quotaUnitsToDollars } from '@/lib/format'

import type { ReferralOverview, UserWalletData } from '../types'

interface ReferralRewardsCardProps {
  overview: ReferralOverview | null
  user: UserWalletData | null
  affiliateLink: string
  onWithdraw: () => void
  loading?: boolean
}

export function ReferralRewardsCard(props: ReferralRewardsCardProps) {
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
        </CardContent>
      </Card>
    )
  }

  const rules = props.overview?.rules
  const ratePercent = rules
    ? Math.round((rules.affiliate_reward_rate ?? 0) * 100)
    : 20
  const freezeDays = rules?.affiliate_freeze_days ?? 7
  const minWithdrawQuota = rules?.affiliate_min_withdraw_quota ?? 0
  const minWithdrawDollars = quotaUnitsToDollars(minWithdrawQuota)
  const availableQuota = props.overview?.available_quota ?? 0
  const canWithdraw =
    (rules?.affiliate_reward_enabled ?? false) &&
    availableQuota >= minWithdrawQuota
  const legacyAffQuota = props.user?.aff_quota ?? 0

  return (
    <Card data-card-hover='false' className='bg-muted/20 py-0'>
      <CardContent className='grid gap-3 p-3 sm:gap-4 sm:p-4 lg:grid-cols-[minmax(200px,1fr)_minmax(220px,0.8fr)_minmax(280px,1fr)] lg:items-center'>
        <div className='flex min-w-0 items-center gap-2.5'>
          <IconBadge tone='chart-3'>
            <Share2 />
          </IconBadge>
          <div className='min-w-0'>
            <h3 className='truncate text-sm font-semibold'>
              {t('Referral Program')}
            </h3>
            <p className='text-muted-foreground line-clamp-2 text-xs'>
              {t(
                'Earn {{rate}}% of paid consumption from referred users. Rewards unlock after {{days}} days.',
                {
                  rate: ratePercent,
                  days: freezeDays,
                }
              )}
            </p>
          </div>
        </div>

        <div className='grid grid-cols-3 gap-1.5 text-center'>
          {[
            [t('Pending'), formatQuota(props.overview?.pending_quota ?? 0)],
            [t('Available'), formatQuota(availableQuota)],
            [t('Withdrawn'), formatQuota(props.overview?.withdrawn_quota ?? 0)],
          ].map(([label, value]) => (
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

        <div className='flex flex-col gap-2'>
          <div className='flex items-center gap-2'>
            <Input
              value={props.affiliateLink}
              readOnly
              className='border-muted bg-background/70 h-9 min-w-0 flex-1 font-mono text-xs'
            />
            <CopyButton
              value={props.affiliateLink}
              variant='outline'
              className='bg-background size-9 shrink-0'
              iconClassName='size-4'
              tooltip={t('Copy referral link')}
              aria-label={t('Copy referral link')}
            />
            <Button
              onClick={props.onWithdraw}
              disabled={!canWithdraw}
              className='h-9 shrink-0 px-3'
              size='sm'
            >
              {t('Withdraw')}
            </Button>
          </div>
          <p className='text-muted-foreground/70 text-xs'>
            {t('Minimum withdrawal: {{amount}} ({{quota}})', {
              amount: minWithdrawDollars.toLocaleString(undefined, {
                maximumFractionDigits: 2,
              }),
              quota: formatQuota(minWithdrawQuota),
            })}
            {legacyAffQuota > 0
              ? ` · ${t('Legacy rewards')}: ${formatQuota(legacyAffQuota)}`
              : ''}
          </p>
        </div>
      </CardContent>
    </Card>
  )
}
