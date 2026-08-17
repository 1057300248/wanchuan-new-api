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
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, test } from 'vitest'

const walletDir = resolve(dirname(fileURLToPath(import.meta.url)), '../..')

function source(relativePath: string): string {
  return readFileSync(resolve(walletDir, relativePath), 'utf8')
}

describe('ChuanAPI wallet surface contract', () => {
  test('wallet page composes only safe funding surfaces', () => {
    const wallet = source('index.tsx')
    for (const component of [
      'WalletAccountsCard',
      'RedemptionCard',
      'ReferralRewardsCard',
      'ReferralHistoryCard',
      'LedgerTable',
    ]) {
      expect(wallet).toMatch(new RegExp(`<${component}`))
    }
    expect(wallet).not.toMatch(
      /RechargeFormCard|SubscriptionPlansCard|AffiliateRewardsCard|BillingHistoryDialog|TransferDialog/
    )
  })

  test('ledger filters expose paid and bonus but never affiliate', () => {
    const ledger = source('components/ledger-table.tsx')
    expect(ledger).toMatch(/value: 'paid'/)
    expect(ledger).toMatch(/value: 'bonus'/)
    expect(ledger).not.toMatch(/value: 'affiliate'/)
  })

  test('referral cards contain no withdrawal, transfer, or manual maturity control', () => {
    const referral = [
      source('components/referral-rewards-card.tsx'),
      source('components/referral-history-card.tsx'),
    ].join('\n')
    expect(referral).not.toMatch(/withdraw|transfer|manual mature|mature now/i)
  })

  test('redemption remains feature-gated and independent from payment UI', () => {
    const redemption = source('components/redemption-card.tsx')
    expect(redemption).toMatch(/if \(!props\.enabled\) return null/)
    expect(redemption).toMatch(/useRedemption\(\)/)
    expect(redemption).not.toMatch(/payment|checkout|topup order/i)
  })
})
