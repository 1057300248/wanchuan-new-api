import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { Referrals } from '../index'

const getReferralOverview = vi.fn()
const getReferralRewards = vi.fn()
const getReferralCoins = vi.fn()
const getReferralCoinRewards = vi.fn()
const redeemReferralCoins = vi.fn()

vi.mock('../../wallet/api', () => ({
  getReferralOverview: (...args: unknown[]) => getReferralOverview(...args),
  getReferralRewards: (...args: unknown[]) => getReferralRewards(...args),
  getReferralCoins: (...args: unknown[]) => getReferralCoins(...args),
  getReferralCoinRewards: (...args: unknown[]) =>
    getReferralCoinRewards(...args),
  redeemReferralCoins: (...args: unknown[]) => redeemReferralCoins(...args),
}))

vi.mock('../../wallet/hooks', () => ({
  useAffiliate: () => ({
    affiliateLink: 'https://example.test/sign-up?aff=fixture',
    affiliateCode: 'fixture',
    loading: false,
  }),
}))

describe('referral production readiness', () => {
  beforeEach(() => {
    getReferralOverview.mockResolvedValue({
      success: true,
      data: {
        aff_code: 'fixture',
        invite_count: 1,
        pending_bonus_quota: 0,
        total_bonus_quota: 0,
        reward_rate: 0.2,
        freeze_days: 7,
        rules: { referral_bonus_enabled: false },
      },
    })
    getReferralRewards.mockResolvedValue({
      success: true,
      data: { total: 0, items: [] },
    })
    getReferralCoins.mockResolvedValue({
      success: true,
      data: {
        balance: 1000000,
        pending: 0,
        scale: 1000000,
        redemption_step: 2,
        enabled: true,
        rate: 0.01,
        freeze_days: 7,
      },
    })
    getReferralCoinRewards.mockResolvedValue({
      success: true,
      data: { total: 0, items: [] },
    })
    redeemReferralCoins.mockResolvedValue({ success: false })
  })

  test('redeems one displayed Coin as one million stored microcoins', async () => {
    render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Referrals />
      </QueryClientProvider>
    )
    await waitFor(() =>
      expect(screen.getByText('1.000000')).toBeInTheDocument()
    )

    fireEvent.change(screen.getByRole('textbox', { name: 'Coin units' }), {
      target: { value: '1' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Redeem' }))

    await waitFor(() => expect(redeemReferralCoins).toHaveBeenCalledTimes(1))
    expect(redeemReferralCoins).toHaveBeenCalledWith({
      coins: 1000000,
      request_id: expect.any(String),
    })
  })

  test('shows the active Coin rate, not the retired affiliate rate', async () => {
    render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Referrals />
      </QueryClientProvider>
    )
    await waitFor(() =>
      expect(screen.getByText('1.000000')).toBeInTheDocument()
    )

    expect(screen.queryByText('1.00%')).toBeInTheDocument()
    expect(screen.queryByText('20.00%')).not.toBeInTheDocument()
  })

  test('provides pagination for Coin history without legacy rewards', async () => {
    getReferralCoinRewards.mockResolvedValue({
      success: true,
      data: {
        total: 9,
        items: Array.from({ length: 8 }, (_, index) => ({
          id: index + 1,
          source_user_id: 2,
          paid_amount: 50000000,
          coin_amount: 1000000,
          status: 'credited',
          created_at: 1789200000,
          unlock_at: 1789200000,
        })),
      },
    })
    render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Referrals />
      </QueryClientProvider>
    )
    await waitFor(() => expect(screen.getAllByText('Success')).toHaveLength(8))

    fireEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
    await waitFor(() =>
      expect(getReferralCoinRewards).toHaveBeenCalledWith(2, 8)
    )
    expect(getReferralRewards).toHaveBeenLastCalledWith(1, 8)
  })

  test('preserves decimal Coin input instead of truncating it', async () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <Referrals />
      </QueryClientProvider>
    )
    await screen.findByText('1.000000')
    fireEvent.change(screen.getByRole('textbox', { name: 'Coin units' }), {
      target: { value: '0.5' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Redeem' }))
    await waitFor(() =>
      expect(redeemReferralCoins).toHaveBeenCalledWith({
        coins: 500000,
        request_id: expect.any(String),
      })
    )
  })

  test('disables invalid precision and amounts exceeding the actual balance', async () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <Referrals />
      </QueryClientProvider>
    )
    await screen.findByText('1.000000')
    const input = screen.getByRole('textbox', { name: 'Coin units' })
    for (const value of [
      '0.000001',
      '0.0000001',
      '-1',
      '1.1',
      '1x',
      '9007199254.740992',
    ]) {
      fireEvent.change(input, { target: { value } })
      expect(screen.getByRole('button', { name: 'Redeem' })).toBeDisabled()
    }
    expect(redeemReferralCoins).not.toHaveBeenCalled()
  })

  test('retains the same idempotency key after an uncertain network failure', async () => {
    redeemReferralCoins
      .mockRejectedValueOnce(new Error('connection interrupted'))
      .mockResolvedValueOnce({ success: true })
    render(
      <QueryClientProvider client={new QueryClient()}>
        <Referrals />
      </QueryClientProvider>
    )
    await screen.findByText('1.000000')
    fireEvent.change(screen.getByRole('textbox', { name: 'Coin units' }), {
      target: { value: '1' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Redeem' }))
    await waitFor(() => expect(redeemReferralCoins).toHaveBeenCalledTimes(1))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Redeem' })).toBeEnabled()
    )
    const firstRequest = redeemReferralCoins.mock.calls[0][0]
    fireEvent.click(screen.getByRole('button', { name: 'Redeem' }))
    await waitFor(() => expect(redeemReferralCoins).toHaveBeenCalledTimes(2))
    expect(redeemReferralCoins.mock.calls[1][0]).toEqual(firstRequest)
    await waitFor(() =>
      expect(screen.getByRole('textbox', { name: 'Coin units' })).toHaveValue(
        ''
      )
    )
  })

  test('keeps redemption available when new rewards are disabled', async () => {
    getReferralCoins.mockResolvedValue({
      success: true,
      data: {
        balance: 1000000,
        pending: 0,
        scale: 1000000,
        redemption_step: 2,
        enabled: false,
        rate: 0.01,
        freeze_days: 3,
      },
    })
    render(
      <QueryClientProvider client={new QueryClient()}>
        <Referrals />
      </QueryClientProvider>
    )
    await screen.findByText('1.000000')
    expect(screen.getByText('Freeze days: 3')).toBeInTheDocument()
    fireEvent.change(screen.getByRole('textbox', { name: 'Coin units' }), {
      target: { value: '1' },
    })
    expect(screen.getByRole('button', { name: 'Redeem' })).toBeEnabled()
  })
})
