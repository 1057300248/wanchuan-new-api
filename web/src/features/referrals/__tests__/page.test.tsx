import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { Referrals } from '../index'

const getReferralOverview = vi.fn()
const getReferralRewards = vi.fn()

vi.mock('../../wallet/api', () => ({
  getReferralOverview: (...args: unknown[]) => getReferralOverview(...args),
  getReferralRewards: (...args: unknown[]) => getReferralRewards(...args),
}))

vi.mock('../../wallet/hooks', () => ({
  useAffiliate: () => ({
    affiliateLink: 'https://example.test/sign-up?aff=demo',
    affiliateCode: 'demo',
    loading: false,
    copyAffiliateLink: vi.fn(),
    refetch: vi.fn(),
  }),
}))

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
        reward_rate: 0.2,
        freeze_days: 7,
        rules: {
          signup_bonus_enabled: true,
          signup_bonus_quota: 0,
          referral_bonus_enabled: true,
          referral_bonus_rate: 0.2,
          referral_bonus_freeze_days: 7,
        },
      },
    })
    getReferralRewards.mockResolvedValue({
      success: true,
      data: {
        total: 1,
        items: [{
          id: 1,
          user_id: 9,
          source_user_id: 10,
          eligible_paid_quota: 500000,
          reward_quota: 100000,
          status: 'pending',
          unlock_at: 1735689600,
          reference_type: 'topup',
          reference_id: 'order-1',
          created_at: 1735603200,
          unlocked_at: 0,
        }],
      },
    })
  })

  test('loads overview and renders pending reward status', async () => {
    render(<QueryClientProvider client={new QueryClient()}><Referrals /></QueryClientProvider>)

    await waitFor(() => expect(screen.getByText('2')).toBeInTheDocument())
    expect(screen.getByText('Invited Users')).toBeInTheDocument()
    expect(screen.getAllByText('Pending').length).toBeGreaterThan(0)
    expect(getReferralOverview).toHaveBeenCalledTimes(1)
    expect(getReferralRewards).toHaveBeenCalledWith(1, 8)
  })
})
