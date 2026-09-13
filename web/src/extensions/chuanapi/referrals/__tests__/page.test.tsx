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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { Referrals } from '../index'

const getReferralOverview = vi.fn()
const getReferralRewards = vi.fn()
const getReferralInvitees = vi.fn()
const downloadReferralPoster = vi.fn()

vi.mock('@/extensions/chuanapi/wallet/api', () => ({
  getReferralOverview: (...args: unknown[]) => getReferralOverview(...args),
  getReferralRewards: (...args: unknown[]) => getReferralRewards(...args),
  getReferralInvitees: (...args: unknown[]) => getReferralInvitees(...args),
}))

vi.mock('@/extensions/chuanapi/wallet/hooks', () => ({
  useAffiliate: () => ({
    affiliateLink: 'https://example.test/sign-up?aff=demo',
    affiliateCode: 'demo',
    loading: false,
    copyAffiliateLink: vi.fn(),
    refetch: vi.fn(),
  }),
}))

vi.mock('../lib/poster', () => ({
  downloadReferralPoster: (...args: unknown[]) =>
    downloadReferralPoster(...args),
}))

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <Referrals />
    </QueryClientProvider>
  )
}

describe('referral program page', () => {
  beforeEach(() => {
    getReferralOverview.mockResolvedValue({
      success: true,
      data: {
        aff_code: 'demo',
        inviter_id: 0,
        invite_count: 2,
        pending_count: 1,
        eligible_paid_quota: 500000,
        pending_bonus_quota: 100000,
        credited_bonus_quota: 200000,
        total_bonus_quota: 300000,
        reward_rate: 0.05,
        freeze_days: 7,
        rules: {
          signup_bonus_enabled: true,
          signup_bonus_quota: 0,
          referral_bonus_enabled: true,
          referral_bonus_rate: 0.05,
          referral_bonus_freeze_days: 7,
        },
      },
    })
    getReferralInvitees.mockResolvedValue({
      success: true,
      data: {
        total: 1,
        items: [
          {
            user_id: 9,
            username: 'invitee-one',
            status: 1,
            created_at: 1735603200,
            eligible_paid_quota: 500000,
            reward_quota: 25000,
            reward_status: 'pending',
          },
        ],
      },
    })
    getReferralRewards.mockResolvedValue({
      success: true,
      data: {
        total: 1,
        items: [
          {
            id: 1,
            user_id: 8,
            source_user_id: 9,
            eligible_paid_quota: 500000,
            reward_quota: 25000,
            status: 'pending',
            unlock_at: 1735689600,
            reference_type: 'topup',
            reference_id: 'order-1',
            created_at: 1735603200,
            unlocked_at: 0,
          },
        ],
      },
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  test('renders invite link, stats, and invitee progress', async () => {
    renderPage()

    expect(await screen.findByText('invitee-one')).toBeInTheDocument()
    expect(screen.getByText('Invited Users')).toBeInTheDocument()
    expect(screen.getByText('5%')).toBeInTheDocument()
    expect(
      screen.getAllByText('https://example.test/sign-up?aff=demo').length
    ).toBeGreaterThan(0)
    expect(getReferralInvitees).toHaveBeenCalledWith(1, 8)
    expect(getReferralRewards).toHaveBeenCalledWith(1, 8)
  })

  test('downloads the poster as an image instead of printing', async () => {
    const printSpy = vi.fn()
    vi.stubGlobal('print', printSpy)
    renderPage()

    await userEvent.click(
      await screen.findByRole('button', { name: 'Download poster' })
    )

    await waitFor(() => expect(downloadReferralPoster).toHaveBeenCalledTimes(1))
    expect(downloadReferralPoster).toHaveBeenCalledWith(
      expect.objectContaining({ link: 'https://example.test/sign-up?aff=demo' })
    )
    expect(printSpy).not.toHaveBeenCalled()
  })

  test('shows promotion copy limited to the first three top-ups', async () => {
    renderPage()

    await userEvent.click(
      await screen.findByRole('tab', { name: 'Promotion copy' })
    )

    expect(await screen.findByText('Promotion copy 1')).toBeInTheDocument()
    expect(
      screen.getByText(/no referral rewards from the fourth top-up/i)
    ).toBeInTheDocument()
    expect(
      screen.getByText(/fourth top-up onwards has no rewards/i)
    ).toBeInTheDocument()
  })
})
