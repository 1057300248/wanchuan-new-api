/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import {
  Activity,
  AlertTriangle,
  ArrowUpRight,
  Clock3,
  Coins,
  FileText,
  Gauge,
  Gift,
  KeyRound,
  Layers3,
  WalletCards,
} from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { getUserLogs, getUserLogStats } from '@/features/usage-logs/api'
import type { UsageLog } from '@/features/usage-logs/data/schema'
import { useWalletSummary } from '@/features/wallet/hooks/use-wallet'
import { formatQuota, formatTimestampRelative } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { getFlowQuotaDates, getUserQuotaDates } from '../../api'
import {
  aggregateOverviewTopModels,
  aggregateOverviewTrend,
  buildOverviewTimeWindow,
  type OverviewRangeDays,
} from './overview-metrics'

const RANGE_OPTIONS: readonly OverviewRangeDays[] = [1, 7, 30]
const TOP_MODEL_SKELETON_KEYS = [
  'top-model-1',
  'top-model-2',
  'top-model-3',
  'top-model-4',
  'top-model-5',
] as const
const RECENT_REQUEST_SKELETON_KEYS = [
  'recent-request-1',
  'recent-request-2',
  'recent-request-3',
  'recent-request-4',
  'recent-request-5',
] as const

function rangeLabel(days: OverviewRangeDays): string {
  if (days === 1) return '24h'
  return `${days}d`
}

function MetricCard(props: {
  label: string
  value: string
  description: string
  icon: typeof Activity
  loading?: boolean
}) {
  const Icon = props.icon
  return (
    <Card size='sm'>
      <CardHeader>
        <CardDescription className='flex items-center gap-2'>
          <Icon className='size-4' aria-hidden='true' />
          {props.label}
        </CardDescription>
        <CardTitle className='font-mono text-xl tabular-nums'>
          {props.loading ? <Skeleton className='h-7 w-24' /> : props.value}
        </CardTitle>
      </CardHeader>
      <CardContent className='text-muted-foreground text-xs'>
        {props.description}
      </CardContent>
    </Card>
  )
}

export function OverviewDashboard() {
  const { t, i18n } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const quotaPerUnit = useSystemConfigStore(
    (state) => state.config.currency.quotaPerUnit
  )
  const { summary: wallet, loading: walletLoading } = useWalletSummary()
  const [rangeDays, setRangeDays] = useState<OverviewRangeDays>(7)
  const timeWindow = useMemo(
    () => buildOverviewTimeWindow(rangeDays),
    [rangeDays]
  )

  const statsQuery = useQuery({
    queryKey: ['customer-overview', 'stats', rangeDays],
    queryFn: () =>
      getUserLogStats({
        start_timestamp: timeWindow.start,
        end_timestamp: timeWindow.end,
      }),
  })
  const trendQuery = useQuery({
    queryKey: ['customer-overview', 'trend', rangeDays],
    queryFn: () =>
      getUserQuotaDates({
        start_timestamp: timeWindow.start,
        end_timestamp: timeWindow.end,
      }),
  })
  const flowQuery = useQuery({
    queryKey: ['customer-overview', 'models', rangeDays],
    queryFn: () =>
      getFlowQuotaDates({
        start_timestamp: timeWindow.start,
        end_timestamp: timeWindow.end,
      }),
  })
  const recentQuery = useQuery({
    queryKey: ['customer-overview', 'recent', rangeDays],
    queryFn: () =>
      getUserLogs({
        p: 1,
        page_size: 5,
        start_timestamp: timeWindow.start,
        end_timestamp: timeWindow.end,
      }),
  })

  const stats = statsQuery.data?.data
  const trend = useMemo(
    () => aggregateOverviewTrend(trendQuery.data?.data ?? []),
    [trendQuery.data]
  )
  const topModels = useMemo(
    () => aggregateOverviewTopModels(flowQuery.data?.data ?? []),
    [flowQuery.data]
  )
  const recentLogs = (recentQuery.data?.data?.items ?? []) as UsageLog[]
  const maxTrendQuota = Math.max(1, ...trend.map((point) => point.quota))
  const maxModelQuota = Math.max(1, ...topModels.map((model) => model.quota))
  const spendable = wallet?.spendable ?? 0
  const lowBalance = !walletLoading && spendable <= Math.max(1, quotaPerUnit)
  const requestCount = Number(stats?.request_count ?? 0)
  const tokenCount = Number(stats?.token_count ?? 0)
  const spend = Number(stats?.quota ?? 0)

  let trendContent: ReactNode
  if (trendQuery.isLoading) {
    trendContent = <Skeleton className='h-56 w-full' />
  } else if (trend.length === 0) {
    trendContent = (
      <div className='text-muted-foreground flex h-56 items-center justify-center text-sm'>
        {t('No usage in this time range')}
      </div>
    )
  } else {
    trendContent = (
      <div
        className='flex h-56 items-end gap-1.5'
        aria-label={t('Usage Trend')}
      >
        {trend.map((point) => (
          <div
            key={point.timestamp}
            className='group flex min-w-0 flex-1 flex-col items-center justify-end gap-2'
            title={`${formatQuota(point.quota)} · ${point.requests} ${t('Requests')}`}
          >
            <div
              className='bg-primary/75 group-hover:bg-primary w-full min-w-1 rounded-t transition-colors'
              style={{
                height: `${Math.max(4, (point.quota / maxTrendQuota) * 184)}px`,
              }}
            />
            <span className='text-muted-foreground hidden max-w-full truncate text-[10px] tabular-nums sm:block'>
              {new Date(point.timestamp * 1000).toLocaleDateString(undefined, {
                month: 'numeric',
                day: 'numeric',
              })}
            </span>
          </div>
        ))}
      </div>
    )
  }

  let topModelsContent: ReactNode
  if (flowQuery.isLoading) {
    topModelsContent = TOP_MODEL_SKELETON_KEYS.map((key) => (
      <Skeleton key={key} className='h-10 w-full' />
    ))
  } else if (topModels.length === 0) {
    topModelsContent = (
      <div className='text-muted-foreground py-16 text-center text-sm'>
        {t('No model usage yet')}
      </div>
    )
  } else {
    topModelsContent = topModels.map((model) => (
      <div key={model.name} className='space-y-1.5'>
        <div className='flex items-center justify-between gap-3 text-sm'>
          <span className='truncate font-medium'>{model.name}</span>
          <span className='text-muted-foreground shrink-0 font-mono text-xs tabular-nums'>
            {formatQuota(model.quota)}
          </span>
        </div>
        <div className='bg-muted h-1.5 overflow-hidden rounded-full'>
          <div
            className='bg-primary h-full rounded-full'
            style={{
              width: `${Math.max(3, (model.quota / maxModelQuota) * 100)}%`,
            }}
          />
        </div>
      </div>
    ))
  }

  let recentRequestsContent: ReactNode
  if (recentQuery.isLoading) {
    recentRequestsContent = (
      <div className='space-y-2'>
        {RECENT_REQUEST_SKELETON_KEYS.map((key) => (
          <Skeleton key={key} className='h-12 w-full' />
        ))}
      </div>
    )
  } else if (recentLogs.length === 0) {
    recentRequestsContent = (
      <div className='text-muted-foreground py-10 text-center text-sm'>
        {t('No recent requests')}
      </div>
    )
  } else {
    recentRequestsContent = (
      <div className='divide-y'>
        {recentLogs.map((log) => {
          const tokens = log.prompt_tokens + log.completion_tokens
          const isError = log.type === 5
          return (
            <div
              key={`${log.request_id}-${log.id}`}
              className='grid gap-2 py-3 text-sm sm:grid-cols-[minmax(0,1fr)_auto_auto_auto] sm:items-center sm:gap-4'
            >
              <div className='min-w-0'>
                <div className='flex items-center gap-2'>
                  <span
                    className={cn(
                      'size-2 shrink-0 rounded-full',
                      isError ? 'bg-destructive' : 'bg-success'
                    )}
                  />
                  <span className='truncate font-medium'>
                    {log.model_name || t('Unknown model')}
                  </span>
                </div>
                <div className='text-muted-foreground mt-1 flex items-center gap-1.5 text-xs'>
                  <Clock3 className='size-3' />
                  {formatTimestampRelative(
                    log.created_at,
                    'seconds',
                    i18n.language
                  )}
                </div>
              </div>
              <div className='text-muted-foreground font-mono text-xs tabular-nums'>
                {tokens.toLocaleString()} {t('Tokens')}
              </div>
              <div className='font-mono text-xs tabular-nums'>
                {formatQuota(log.quota)}
              </div>
              <div className='text-muted-foreground flex items-center gap-1 font-mono text-xs tabular-nums'>
                <FileText className='size-3' />
                {log.request_id?.slice(0, 12) || '-'}
              </div>
            </div>
          )
        })}
      </div>
    )
  }

  return (
    <div className='flex flex-col gap-4' data-customer-overview>
      <div className='flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between'>
        <div>
          <h1 className='text-2xl font-semibold tracking-tight'>
            {t('Overview')}
          </h1>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t('Usage, balance, and recent API activity in one place')}
          </p>
        </div>
        <div className='flex flex-wrap items-center gap-2'>
          <div
            className='bg-muted flex rounded-lg p-1'
            aria-label={t('Time range')}
          >
            {RANGE_OPTIONS.map((days) => (
              <Button
                key={days}
                size='sm'
                variant={rangeDays === days ? 'default' : 'ghost'}
                className='h-7 min-w-12 px-2'
                aria-pressed={rangeDays === days}
                onClick={() => setRangeDays(days)}
              >
                {rangeLabel(days)}
              </Button>
            ))}
          </div>
          <Button variant='outline' render={<a href='/docs' />}>
            {t('View Docs')}
            <ArrowUpRight data-icon='inline-end' />
          </Button>
          <Button
            render={<Link to='/dashboard/keys' search={{ create: true }} />}
          >
            <KeyRound data-icon='inline-start' />
            {t('Create API Key')}
          </Button>
        </div>
      </div>

      {lowBalance && (
        <div className='border-warning/30 bg-warning/10 text-warning-foreground flex flex-col gap-3 rounded-xl border p-4 sm:flex-row sm:items-center sm:justify-between'>
          <div className='flex items-start gap-3'>
            <AlertTriangle className='mt-0.5 size-5 shrink-0' />
            <div>
              <div className='font-medium'>{t('Low balance')}</div>
              <div className='text-sm opacity-80'>
                {t(
                  'Spendable balance is at or below one usage unit. Add credits before production traffic.'
                )}
              </div>
            </div>
          </div>
          <Button size='sm' render={<Link to='/dashboard/wallet' />}>
            {t('Add credits')}
          </Button>
        </div>
      )}

      <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4'>
        <MetricCard
          label={t('Spendable Balance')}
          value={formatQuota(spendable)}
          description={t(
            'Paid plus bonus balance; affiliate rewards are withdrawable only'
          )}
          icon={WalletCards}
          loading={walletLoading}
        />
        <MetricCard
          label={t('Bonus Balance')}
          value={formatQuota(wallet?.bonus ?? 0)}
          description={t('Used only by bonus-eligible models')}
          icon={Gift}
          loading={walletLoading}
        />
        <MetricCard
          label={t('Affiliate Balance')}
          value={formatQuota(wallet?.withdrawable ?? 0)}
          description={t('Withdrawable referral rewards; not API credit')}
          icon={Coins}
          loading={walletLoading}
        />
        <MetricCard
          label={t('Rate Limit Tier')}
          value={user?.group || 'default'}
          description={t(
            'RPM and TPM are enforced by the current account group'
          )}
          icon={Gauge}
        />
      </div>

      <div className='grid gap-3 sm:grid-cols-3'>
        <MetricCard
          label={t('Requests')}
          value={requestCount.toLocaleString()}
          description={`${rangeLabel(rangeDays)} ${t('request volume')}`}
          icon={Activity}
          loading={statsQuery.isLoading}
        />
        <MetricCard
          label={t('Tokens')}
          value={tokenCount.toLocaleString()}
          description={`${rangeLabel(rangeDays)} ${t('input and output tokens')}`}
          icon={Layers3}
          loading={statsQuery.isLoading}
        />
        <MetricCard
          label={t('Spend')}
          value={formatQuota(spend)}
          description={`${rangeLabel(rangeDays)} ${t('wallet or subscription usage')}`}
          icon={Coins}
          loading={statsQuery.isLoading}
        />
      </div>

      <div className='grid gap-4 xl:grid-cols-[minmax(0,1.6fr)_minmax(320px,1fr)]'>
        <Card>
          <CardHeader>
            <CardTitle>{t('Usage Trend')}</CardTitle>
            <CardDescription>
              {t('Spend over the selected time range')}
            </CardDescription>
          </CardHeader>
          <CardContent>{trendContent}</CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{t('Top Models')}</CardTitle>
            <CardDescription>
              {t('Models ranked by spend in the selected range')}
            </CardDescription>
          </CardHeader>
          <CardContent className='space-y-4'>{topModelsContent}</CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t('Recent Requests')}</CardTitle>
          <CardDescription>
            {t('Latest API activity for the selected time range')}
          </CardDescription>
          <CardAction>
            <Button
              variant='ghost'
              size='sm'
              render={
                <Link
                  to='/dashboard/logs/$section'
                  params={{ section: 'common' }}
                />
              }
            >
              {t('View all')}
              <ArrowUpRight data-icon='inline-end' />
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent>{recentRequestsContent}</CardContent>
      </Card>
    </div>
  )
}
