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
import { render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import type { ReferralOverview } from '../../types'
import { AffiliateRewardsCard } from '../affiliate-rewards-card'

const affiliateLink = 'https://example.test/sign-up?aff=demo'

const overview: ReferralOverview = {
  aff_code: 'demo',
  inviter_id: 0,
  invite_count: 2,
  pending_count: 1,
  eligible_paid_quota: 200000,
  pending_bonus_quota: 100000,
  credited_bonus_quota: 200000,
  total_bonus_quota: 300000,
  reward_rate: 0.05,
  freeze_days: 7,
  rules: {
    signup_bonus_enabled: false,
    signup_bonus_quota: 0,
    referral_bonus_enabled: true,
    referral_bonus_rate: 0.05,
    referral_bonus_freeze_days: 7,
  },
}

describe('affiliate rewards card', () => {
  test('shows the referral link, invite stats, and the first-three-top-ups reward rule', () => {
    render(
      <AffiliateRewardsCard
        overview={overview}
        affiliateLink={affiliateLink}
        loading={false}
      />
    )

    expect(screen.getByDisplayValue(affiliateLink)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Copy referral link' })
    ).toBeInTheDocument()
    expect(screen.getByText('Invites')).toBeInTheDocument()
    expect(screen.getByText('2')).toBeInTheDocument()

    expect(screen.getByText(/first 3 top-ups/)).toHaveTextContent(
      "For each invited friend's first 3 top-ups, you earn 5.0% of the top-up amount as a reward."
    )
  })

  test('renders a 0% reward rate when referral rewards are disabled', () => {
    render(
      <AffiliateRewardsCard
        overview={{ ...overview, reward_rate: 0 }}
        affiliateLink={affiliateLink}
        loading={false}
      />
    )

    expect(screen.getByText(/first 3 top-ups/)).toHaveTextContent(
      'you earn 0% of the top-up amount as a reward'
    )
  })

  test('replaces the stats and referral link with loading placeholders while data is pending', () => {
    const { container } = render(
      <AffiliateRewardsCard overview={null} affiliateLink='' loading />
    )

    expect(container.querySelectorAll('[data-slot="skeleton"]')).toHaveLength(3)
    expect(screen.getByPlaceholderText('Loading...')).toBeInTheDocument()
  })
})
