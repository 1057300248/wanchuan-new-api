/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { Share2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Card, CardContent } from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota } from '@/lib/format'

import { getReferralOverview } from '../wallet/api'
import { useAffiliate } from '../wallet/hooks'

function formatRate(rate: number): string {
  if (!Number.isFinite(rate) || rate <= 0) return '0%'
  const percent = rate * 100
  return Number.isInteger(percent)
    ? `${percent}%`
    : `${percent.toFixed(2).replace(/0+$/, '').replace(/\.$/, '')}%`
}

export function Referrals() {
  const { t } = useTranslation()
  const { affiliateLink, loading: affiliateLoading } = useAffiliate()
  const overviewQuery = useQuery({
    queryKey: ['referrals', 'overview'],
    queryFn: getReferralOverview,
  })
  const overview = overviewQuery.data?.success
    ? overviewQuery.data.data
    : undefined

  if (overviewQuery.isError || overviewQuery.data?.success === false) {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Referral Program')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <ErrorState onRetry={() => void overviewQuery.refetch()} />
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }

  const loading = overviewQuery.isPending || affiliateLoading
  const stats = [
    [t('Pending'), formatQuota(overview?.pending_bonus_quota ?? 0)],
    [t('Total Earned'), formatQuota(overview?.total_bonus_quota ?? 0)],
    [t('Invites'), String(overview?.invite_count ?? 0)],
  ]

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Referral Program')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <Card data-card-hover='false' className='bg-muted/20 py-0'>
          <CardContent className='grid gap-3 p-3 sm:gap-4 sm:p-4 lg:grid-cols-[minmax(200px,1fr)_minmax(180px,0.65fr)_minmax(280px,1fr)] lg:items-center'>
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
                    'Earn rewards when users join through your referral link. Transfer accumulated rewards to your balance anytime.'
                  )}
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
                    {loading ? (
                      <Skeleton className='mx-auto h-5 w-12' />
                    ) : (
                      value
                    )}
                  </div>
                </div>
              ))}
            </div>

            <div className='flex items-center gap-2'>
              <Input
                value={affiliateLink}
                readOnly
                placeholder={loading ? t('Loading...') : undefined}
                className='border-muted bg-background/70 h-9 min-w-0 flex-1 font-mono text-xs'
              />
              <CopyButton
                value={affiliateLink}
                variant='outline'
                className='bg-background size-9 shrink-0'
                iconClassName='size-4'
                tooltip={t('Copy referral link')}
                aria-label={t('Copy referral link')}
              />
            </div>

            <p className='text-muted-foreground text-xs lg:col-span-3'>
              {t('Referral reward rate')}:{' '}
              {formatRate(overview?.reward_rate ?? 0)}
              {' · '}
              {t('Freeze days')}: {overview?.freeze_days ?? 0}
            </p>
          </CardContent>
        </Card>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
