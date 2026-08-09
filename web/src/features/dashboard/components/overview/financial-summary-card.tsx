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
import { Link } from '@tanstack/react-router'
import {
  ArrowRight,
  CircleDollarSign,
  Gift,
  HandCoins,
  Share2,
  WalletCards,
} from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import {
  useWalletSummary,
  useReferralOverview,
} from '@/features/wallet/hooks/use-wallet'
import { cn } from '@/lib/utils'

import {
  buildFinancialSummaryView,
  type FinancialSummaryView,
} from '../../lib/wallet-summary'

const ACCOUNT_ICONS: Record<
  'spendable' | 'paid' | 'bonus' | 'affiliate',
  typeof WalletCards
> = {
  spendable: WalletCards,
  paid: CircleDollarSign,
  bonus: Gift,
  affiliate: HandCoins,
}

const ACCOUNT_TONES: Record<
  'spendable' | 'paid' | 'bonus' | 'affiliate',
  IconBadgeTone
> = {
  spendable: 'success',
  paid: 'info',
  bonus: 'chart-2',
  affiliate: 'chart-3',
}

function LoadingBlock() {
  return (
    <div className='p-3 sm:p-5'>
      <div className='flex flex-col gap-1'>
        <Skeleton className='h-5 w-36' />
        <Skeleton className='h-4 w-56' />
      </div>
      <div className='mt-4 grid grid-cols-2 gap-3 lg:grid-cols-4'>
        {['spendable', 'paid', 'bonus', 'affiliate'].map((key) => (
          <div key={key} className='rounded-lg border p-3 sm:p-4'>
            <Skeleton className='h-3.5 w-20' />
            <Skeleton className='mt-2 h-6 w-full' />
            <Skeleton className='mt-1.5 h-3 w-24' />
          </div>
        ))}
      </div>
    </div>
  )
}

function FailedBlock(props: { onRetry: () => void }) {
  const { t } = useTranslation()

  return (
    <div className='flex flex-wrap items-center justify-between gap-3 p-3 sm:p-5'>
      <div className='flex min-w-0 items-center gap-2.5'>
        <IconBadge tone='warning'>
          <WalletCards />
        </IconBadge>
        <div className='min-w-0'>
          <div className='text-sm font-semibold'>
            {t('Wallet summary unavailable')}
          </div>
          <div className='text-muted-foreground text-xs'>
            {t('Could not load wallet data')}
          </div>
        </div>
      </div>
      <Button size='sm' variant='outline' onClick={props.onRetry}>
        {t('Retry')}
      </Button>
    </div>
  )
}

function ReadyBlock(props: { view: FinancialSummaryView }) {
  const { t } = useTranslation()
  const view = props.view
  const referral = view.referral

  return (
    <div className='flex flex-col gap-4 p-3 sm:p-5'>
      <div className='flex flex-wrap items-start justify-between gap-2'>
        <div className='flex flex-col gap-0.5'>
          <h3 className='text-sm font-semibold sm:text-base'>
            {t('Financial Summary')}
          </h3>
          <p className='text-muted-foreground text-xs sm:text-sm'>
            {t('Account balances and referral rewards')}
          </p>
        </div>
        <Button
          variant='ghost'
          size='sm'
          className='text-muted-foreground h-7 gap-1.5 px-2 text-xs'
          render={<Link to='/dashboard/wallet' />}
        >
          {t('View wallet')}
          <ArrowRight data-icon='inline-end' />
        </Button>
      </div>

      {view.accounts.length > 0 && (
        <div
          data-financial-summary='accounts'
          className='grid grid-cols-2 gap-3 lg:grid-cols-4'
        >
          {view.accounts.map((account) => {
            const Icon = ACCOUNT_ICONS[account.key]

            return (
              <div
                key={account.key}
                data-financial-summary-account={account.key}
                className='rounded-lg border p-3 sm:p-4'
              >
                <div className='flex items-center gap-2'>
                  <IconBadge tone={ACCOUNT_TONES[account.key]} size='sm'>
                    <Icon />
                  </IconBadge>
                  <div className='text-muted-foreground truncate text-[11px] font-medium tracking-wider uppercase sm:text-xs'>
                    {t(account.labelKey)}
                  </div>
                </div>
                <div className='text-foreground mt-2 font-mono text-base font-bold tracking-tight break-all tabular-nums sm:text-xl'>
                  {account.value}
                </div>
                <div className='text-muted-foreground/60 mt-0.5 hidden text-xs md:block'>
                  {t(account.descriptionKey)}
                </div>
              </div>
            )
          })}
        </div>
      )}

      {referral && (
        <div
          data-financial-summary='referral'
          className={cn(
            'bg-muted/40 flex flex-col gap-3 rounded-xl p-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4 sm:p-4'
          )}
        >
          <div className='flex min-w-0 items-center gap-2.5'>
            <IconBadge tone='chart-3' size='sm'>
              <Share2 />
            </IconBadge>
            <div className='min-w-0'>
              <div className='truncate text-sm font-semibold'>
                {t('Referral Program')}
              </div>
              <div className='text-muted-foreground truncate text-xs'>
                {t('Reward rate: {{rate}}%', { rate: referral.ratePercent })}
                {' · '}
                {t('Unlock period: {{days}} days', {
                  days: referral.freezeDays,
                })}
              </div>
            </div>
          </div>

          <div
            data-financial-summary='referral-stats'
            className='grid grid-cols-3 gap-1.5 text-center'
          >
            <div className='min-w-16'>
              <div className='text-muted-foreground truncate text-[10px] font-medium tracking-wider uppercase'>
                {t('Pending')}
              </div>
              <div className='mt-0.5 truncate text-sm font-semibold tabular-nums'>
                {referral.pending}
              </div>
            </div>
            <div className='min-w-16'>
              <div className='text-muted-foreground truncate text-[10px] font-medium tracking-wider uppercase'>
                {t('Available')}
              </div>
              <div className='mt-0.5 truncate text-sm font-semibold tabular-nums'>
                {referral.available}
              </div>
            </div>
            <div className='min-w-16'>
              <div className='text-muted-foreground truncate text-[10px] font-medium tracking-wider uppercase'>
                {t('Invited users')}
              </div>
              <div className='mt-0.5 truncate text-sm font-semibold tabular-nums'>
                {referral.invitedCount}
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

/**
 * P1 financial summary for the customer overview: paid/bonus/affiliate
 * account balances plus the referral pending/available summary and rules.
 * Loading shows skeletons; failures degrade to a compact retry card that
 * never blocks the rest of the first screen.
 */
export function FinancialSummaryCard() {
  const { t } = useTranslation()
  const {
    summary,
    loading: walletLoading,
    refetch: refetchWallet,
  } = useWalletSummary()
  const {
    overview,
    loading: referralLoading,
    refetch: refetchReferral,
  } = useReferralOverview()

  const view = useMemo(
    () =>
      buildFinancialSummaryView({
        summary,
        overview,
        loading: walletLoading || referralLoading,
      }),
    [overview, referralLoading, summary, walletLoading]
  )

  const handleRetry = () => {
    void refetchWallet()
    void refetchReferral()
  }

  return (
    <section
      aria-label={t('Financial Summary')}
      data-financial-summary-card=''
      data-financial-summary-state={view.status}
      className='bg-card overflow-hidden rounded-2xl border shadow-xs'
    >
      {view.status === 'loading' && <LoadingBlock />}
      {view.status === 'failed' && <FailedBlock onRetry={handleRetry} />}
      {view.status === 'ready' && <ReadyBlock view={view} />}
    </section>
  )
}
