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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { ReferralOverview, WalletSummary } from '@/features/wallet/types'

import { buildFinancialSummaryView } from '../wallet-summary'

const RULES = {
  signup_bonus_enabled: true,
  signup_bonus_quota: 300000,
  affiliate_reward_enabled: true,
  affiliate_reward_rate: 0.2,
  affiliate_freeze_days: 7,
  affiliate_min_withdraw_quota: 100000,
}

const SUMMARY: WalletSummary = {
  total: 1500000,
  spendable: 1300000,
  withdrawable: 200000,
  paid: 1000000,
  bonus: 300000,
  affiliate: 200000,
  rules: RULES,
}

const OVERVIEW: ReferralOverview = {
  aff_code: 'abc123',
  inviter_id: 0,
  invite_count: 3,
  pending_count: 1,
  pending_quota: 50000,
  available_quota: 100000,
  withdrawn_quota: 20000,
  total_earned_quota: 170000,
  rules: RULES,
}

describe('buildFinancialSummaryView', () => {
  test('loading state yields no content until data arrives', () => {
    const view = buildFinancialSummaryView({
      summary: null,
      overview: null,
      loading: true,
    })
    assert.equal(view.status, 'loading')
    assert.deepEqual(view.accounts, [])
    assert.equal(view.referral, null)
  })

  test('failure state when both sources fail without loading', () => {
    const view = buildFinancialSummaryView({
      summary: null,
      overview: null,
      loading: false,
    })
    assert.equal(view.status, 'failed')
    assert.deepEqual(view.accounts, [])
    assert.equal(view.referral, null)
  })

  test('ready state shows all four accounts formatted through the shared quota formatter', () => {
    const view = buildFinancialSummaryView({
      summary: SUMMARY,
      overview: OVERVIEW,
      loading: false,
    })
    assert.equal(view.status, 'ready')

    assert.deepEqual(
      view.accounts.map((account) => account.key),
      ['spendable', 'paid', 'bonus', 'affiliate']
    )
    assert.deepEqual(
      view.accounts.map((account) => account.value),
      ['$3', '$2', '$0.6', '$0.4']
    )
    assert.equal(view.accounts[0].labelKey, 'Total Balance')
    assert.equal(view.accounts[1].labelKey, 'Paid Balance')
    assert.equal(view.accounts[2].labelKey, 'Bonus Balance')
    assert.equal(view.accounts[3].labelKey, 'Affiliate Balance')
  })

  test('ready state exposes referral pending/available, invite count and rules summary', () => {
    const view = buildFinancialSummaryView({
      summary: SUMMARY,
      overview: OVERVIEW,
      loading: false,
    })
    assert.equal(view.status, 'ready')

    assert.ok(view.referral)
    assert.equal(view.referral.pending, '$0.1')
    assert.equal(view.referral.available, '$0.2')
    assert.equal(view.referral.invitedCount, 3)
    assert.equal(view.referral.ratePercent, 20)
    assert.equal(view.referral.freezeDays, 7)
    assert.equal(view.referral.minWithdrawQuota, 100000)
    assert.equal(view.referral.minWithdrawDollars, 0.2)
  })

  test('partial failure keeps the sections that loaded and drops the rest', () => {
    const walletOnly = buildFinancialSummaryView({
      summary: SUMMARY,
      overview: null,
      loading: false,
    })
    assert.equal(walletOnly.status, 'ready')
    assert.equal(walletOnly.accounts.length, 4)
    assert.equal(walletOnly.referral, null)

    const referralOnly = buildFinancialSummaryView({
      summary: null,
      overview: OVERVIEW,
      loading: false,
    })
    assert.equal(referralOnly.status, 'ready')
    assert.deepEqual(referralOnly.accounts, [])
    assert.ok(referralOnly.referral)
  })

  test('missing rule fields fall back to safe defaults instead of breaking the summary', () => {
    const bareOverview: ReferralOverview = {
      ...OVERVIEW,
      rules: {
        signup_bonus_enabled: false,
        signup_bonus_quota: 0,
        affiliate_reward_enabled: false,
        affiliate_reward_rate: 0,
        affiliate_freeze_days: 0,
        affiliate_min_withdraw_quota: 0,
      },
    }
    const view = buildFinancialSummaryView({
      summary: null,
      overview: bareOverview,
      loading: false,
    })
    assert.equal(view.status, 'ready')
    assert.ok(view.referral)
    assert.equal(view.referral.ratePercent, 0)
    assert.equal(view.referral.freezeDays, 0)
    assert.equal(view.referral.minWithdrawQuota, 0)
    assert.equal(view.referral.minWithdrawDollars, 0)
  })
})
