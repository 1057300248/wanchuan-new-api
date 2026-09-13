/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { QRCodeSVG } from 'qrcode.react'
import { Gift, Link2, Users } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { DataTablePagination, useDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { useAuthStore } from '@/stores/auth-store'

import { CopyButton } from '@/components/copy-button'
import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota } from '@/lib/format'

import { getReferralOverview, getReferralRewards } from '../wallet/api'
import { useAffiliate } from '../wallet/hooks'
import type { AffiliateRewardItem } from '../wallet/types'

const PAGE_SIZE = 8

function statusVariant(status: AffiliateRewardItem['status']) {
  if (status === 'credited') return 'default' as const
  if (status === 'cancelled') return 'destructive' as const
  return 'warning' as const
}

function statusLabel(status: AffiliateRewardItem['status'], t: (key: string) => string) {
  if (status === 'credited') return t('Success')
  if (status === 'cancelled') return t('Cancelled')
  return t('Pending')
}

function formatDate(value: number): string {
  if (!value) return '-'
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(
    new Date(value * 1000)
  )
}

function StatCard(props: { icon: typeof Users; label: string; value: string; description: string }) {
  const Icon = props.icon
  return (
    <Card size='sm'>
      <CardContent className='flex items-center gap-3'>
        <div className='bg-primary/10 text-primary flex size-10 shrink-0 items-center justify-center rounded-lg'>
          <Icon className='size-5' aria-hidden='true' />
        </div>
        <div className='min-w-0'>
          <p className='text-muted-foreground text-xs'>{props.label}</p>
          <p className='mt-0.5 truncate text-xl font-semibold tabular-nums'>{props.value}</p>
          <p className='text-muted-foreground truncate text-xs'>{props.description}</p>
        </div>
      </CardContent>
    </Card>
  )
}

function ReferralRewardRows(props: {
  loading: boolean
  rewards: AffiliateRewardItem[]
  t: (key: string) => string
}) {
  if (props.loading && props.rewards.length === 0) {
    return <tr><td colSpan={5} className='px-4 py-10 text-center'><Skeleton className='mx-auto h-5 w-40' /></td></tr>
  }
  if (props.rewards.length === 0) {
    return <tr><td colSpan={5} className='text-muted-foreground px-4 py-10 text-center'>{props.t('Your transaction history will appear here')}</td></tr>
  }
  return props.rewards.map((item) => (
    <tr key={item.id} className='hover:bg-muted/30 transition-colors'>
      <td className='text-muted-foreground px-4 py-3'>{formatDate(item.created_at)}</td>
      <td className='px-4 py-3 font-mono'>{formatQuota(item.eligible_paid_quota)}</td>
      <td className='px-4 py-3 font-mono font-medium'>{formatQuota(item.reward_quota)}</td>
      <td className='px-4 py-3'><Badge variant={statusVariant(item.status)}>{statusLabel(item.status, props.t)}</Badge></td>
      <td className='text-muted-foreground px-4 py-3'>{formatDate(item.unlock_at)}</td>
    </tr>
  ))
}

export function Referrals() {
  const { t } = useTranslation()
  const { affiliateLink, loading: affiliateLoading } = useAffiliate()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const queryClient = useQueryClient()
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: PAGE_SIZE })
  const overviewQuery = useQuery({
    queryKey: ['referrals', userId, 'overview'], queryFn: getReferralOverview,
  })
  const rewardsQuery = useQuery({
    queryKey: ['referrals', userId, 'legacy', pagination],
    queryFn: () => getReferralRewards(pagination.pageIndex + 1, pagination.pageSize),
  })
  const overview = overviewQuery.data?.success ? overviewQuery.data.data : null
  const rewards = rewardsQuery.data?.data?.items ?? []
  const loading = overviewQuery.isPending || rewardsQuery.isPending
  const failed = [overviewQuery, rewardsQuery].some(
    (query) => query.isError || query.data?.success === false
  )
  const { table: legacyTable } = useDataTable({
    data: rewards, columns: [], totalCount: rewardsQuery.data?.data?.total ?? 0,
    manualPagination: true, pagination, onPaginationChange: setPagination,
  })

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Referral Program')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto flex w-full max-w-6xl flex-col gap-5'>
          {failed && <ErrorState onRetry={() => void queryClient.invalidateQueries({ queryKey: ['referrals', userId] })} />}
          <Card className='from-primary/10 via-card to-card overflow-hidden bg-gradient-to-br'>
            <CardContent className='grid gap-6 p-5 md:grid-cols-[1fr_auto] md:items-center md:p-7'>
              <div className='space-y-4'>
                <div>
                  <p className='text-primary mb-1 text-sm font-medium'>{t('Share your link and earn rewards')}</p>
                  <h2 className='text-2xl font-semibold tracking-tight'>{t('Your Referral Link')}</h2>
                  <p className='text-muted-foreground mt-2 max-w-xl text-sm'>{t('Rewards will be added directly to your balance')}</p>
                </div>
                <div className='flex max-w-2xl items-center gap-2'>
                  <div className='bg-background/80 text-muted-foreground flex min-h-10 min-w-0 flex-1 items-center gap-2 rounded-md border px-3 font-mono text-xs'>
                    <Link2 className='size-4 shrink-0' aria-hidden='true' />
                    <span className='truncate'>{affiliateLoading ? t('Loading...') : affiliateLink || '-'}</span>
                  </div>
                  {affiliateLink ? (
                    <CopyButton value={affiliateLink} variant='outline' size='default' tooltip={t('Copy referral link')}>
                      <span className='hidden sm:inline'>{t('Copy')}</span>
                    </CopyButton>
                  ) : (
                    <Button variant='outline' size='default' disabled aria-label={t('Copy referral link')}>
                      {t('Copy')}
                    </Button>
                  )}
                </div>
              </div>
              <div className='bg-background/80 mx-auto rounded-xl border p-3 shadow-sm'>
                {affiliateLink ? <QRCodeSVG value={affiliateLink} size={132} includeMargin aria-label={t('Referral link:')} /> : <Skeleton className='size-[132px]' />}
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>{t('Referral Poster')}</CardTitle>
              <CardDescription>{t('Use the fixed poster template with your QR code and referral link.')}</CardDescription>
            </CardHeader>
            <CardContent>
              <div className='mx-auto flex max-w-sm flex-col items-center gap-3 rounded-xl border bg-muted/20 p-6 text-center'>
                <p className='text-lg font-semibold'>{t('Share and earn 5% on the first three top-ups')}</p>
                {affiliateLink ? <QRCodeSVG value={affiliateLink} size={160} includeMargin aria-label={t('Referral link:')} /> : <Skeleton className='size-40' />}
                <p className='text-muted-foreground break-all font-mono text-xs'>{affiliateLink || '-'}</p>
                <Button type='button' variant='outline' disabled={!affiliateLink} onClick={() => window.print()}>{t('Download poster')}</Button>
              </div>
            </CardContent>
          </Card>

          <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-3'>
            <StatCard icon={Users} label={t('Invited Users')} value={loading ? '—' : String(overview?.invite_count ?? 0)} description={t('Number of users invited')} />
            <StatCard icon={Gift} label={t('Pending')} value={loading ? '—' : formatQuota(overview?.pending_bonus_quota ?? 0)} description={t('Rewards will be added directly to your balance')} />
            <StatCard icon={Gift} label={t('Total Earned')} value={loading ? '—' : formatQuota(overview?.total_bonus_quota ?? 0)} description={t('Total invitation revenue')} />
          </div>

          <Card>
            <CardHeader>
              <CardTitle>{t('Invite Info')}</CardTitle>
              <CardDescription>{t('Track referral rewards and their unlock status.')}</CardDescription>
            </CardHeader>
            <CardContent className='p-0'>
              <div className='overflow-x-auto'>
                <table className='w-full text-sm'>
                  <thead className='bg-muted/40 text-muted-foreground border-y text-left text-xs'>
                    <tr>
                      <th className='px-4 py-3 font-medium'>{t('Date')}</th>
                      <th className='px-4 py-3 font-medium'>{t('Eligible quota')}</th>
                      <th className='px-4 py-3 font-medium'>{t('Reward')}</th>
                      <th className='px-4 py-3 font-medium'>{t('Status')}</th>
                      <th className='px-4 py-3 font-medium'>{t('Unlock date')}</th>
                    </tr>
                  </thead>
                  <tbody className='divide-y'><ReferralRewardRows loading={loading} rewards={rewards} t={t} /></tbody>
                </table>
              </div>
              {legacyTable.getPageCount() > 1 && (
                <div className='border-t px-4 py-3' aria-label={t('Invite Info')}>
                  <DataTablePagination table={legacyTable} />
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

