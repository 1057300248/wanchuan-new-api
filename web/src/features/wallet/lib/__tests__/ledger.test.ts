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

import { ledgerReasonLabelKey, withdrawalStatusLabelKey } from '../ledger'

describe('ledgerReasonLabelKey', () => {
  test('maps known backend reasons to i18n keys', () => {
    assert.equal(ledgerReasonLabelKey('signup_bonus'), 'Signup Bonus')
    assert.equal(
      ledgerReasonLabelKey('billing_pre_consume'),
      'Billing Pre-consumed'
    )
    assert.equal(ledgerReasonLabelKey('affiliate_reward'), 'Referral Reward')
    assert.equal(ledgerReasonLabelKey('withdrawal_hold'), 'Withdrawal Hold')
    assert.equal(
      ledgerReasonLabelKey('withdrawal_reject_refund'),
      'Withdrawal Refund'
    )
  })

  test('passes through unknown reasons for forward compatibility', () => {
    assert.equal(ledgerReasonLabelKey('future_reason'), 'future_reason')
    assert.equal(ledgerReasonLabelKey(''), '')
  })
})

describe('withdrawalStatusLabelKey', () => {
  test('maps backend withdrawal statuses to i18n keys', () => {
    assert.equal(withdrawalStatusLabelKey('pending'), 'Pending Review')
    assert.equal(withdrawalStatusLabelKey('approved'), 'Approved')
    assert.equal(withdrawalStatusLabelKey('rejected'), 'Rejected')
    assert.equal(withdrawalStatusLabelKey('paid'), 'Paid')
  })

  test('passes through unknown statuses', () => {
    assert.equal(withdrawalStatusLabelKey('unknown'), 'unknown')
  })
})
