import type { ReferralOverview, WalletSummary } from '@/features/wallet/types'
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
import { formatQuota, quotaUnitsToDollars } from '@/lib/format'

/**
 * Pure view-builder for the dashboard financial summary.
 *
 * Turns the P1 wallet/referral API data into display-ready rows so the
 * component stays thin and the loading/failure/partial contracts are
 * directly testable. Amounts always go through the shared quota/currency
 * formatting (`formatQuota`), never through hard-coded multipliers.
 */
export interface FinancialSummaryAccount {
  key: 'spendable' | 'paid' | 'bonus' | 'affiliate'
  /** i18n key — English source string */
  labelKey: string
  /** Formatted via `formatQuota` */
  value: string
  /** i18n key — English source string */
  descriptionKey: string
}

export interface FinancialSummaryReferral {
  pending: string
  available: string
  invitedCount: number
  ratePercent: number
  freezeDays: number
  /** Raw quota units; formatted for display by the caller */
  minWithdrawQuota: number
  /** Display-currency equivalent of `minWithdrawQuota` */
  minWithdrawDollars: number
}

export interface FinancialSummaryView {
  status: 'loading' | 'failed' | 'ready'
  accounts: FinancialSummaryAccount[]
  referral: FinancialSummaryReferral | null
}

function buildAccounts(summary: WalletSummary): FinancialSummaryAccount[] {
  return [
    {
      key: 'spendable',
      labelKey: 'Spendable Balance',
      value: formatQuota(summary.spendable),
      descriptionKey:
        'Paid plus bonus balance; affiliate rewards are withdrawable only',
    },
    {
      key: 'paid',
      labelKey: 'Paid Balance',
      value: formatQuota(summary.paid),
      descriptionKey: 'From top-ups and transfers',
    },
    {
      key: 'bonus',
      labelKey: 'Bonus Balance',
      value: formatQuota(summary.bonus),
      descriptionKey: 'Signup bonus and invitee gifts',
    },
    {
      key: 'affiliate',
      labelKey: 'Affiliate Balance',
      value: formatQuota(summary.affiliate),
      descriptionKey: 'Matured referral rewards',
    },
  ]
}

function buildReferral(overview: ReferralOverview): FinancialSummaryReferral {
  const minWithdrawQuota = overview.rules?.affiliate_min_withdraw_quota ?? 0
  return {
    pending: formatQuota(overview.pending_quota),
    available: formatQuota(overview.available_quota),
    invitedCount: overview.invite_count,
    ratePercent: Math.round((overview.rules?.affiliate_reward_rate ?? 0) * 100),
    freezeDays: overview.rules?.affiliate_freeze_days ?? 0,
    minWithdrawQuota,
    minWithdrawDollars: quotaUnitsToDollars(minWithdrawQuota),
  }
}

export function buildFinancialSummaryView(args: {
  summary: WalletSummary | null
  overview: ReferralOverview | null
  loading: boolean
}): FinancialSummaryView {
  if (args.loading) {
    return { status: 'loading', accounts: [], referral: null }
  }

  if (!args.summary && !args.overview) {
    return { status: 'failed', accounts: [], referral: null }
  }

  return {
    status: 'ready',
    accounts: args.summary ? buildAccounts(args.summary) : [],
    referral: args.overview ? buildReferral(args.overview) : null,
  }
}
