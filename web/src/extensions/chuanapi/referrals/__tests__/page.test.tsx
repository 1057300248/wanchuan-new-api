/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { Referrals } from '../index'

const getReferralOverview = vi.fn()

vi.mock('@/extensions/chuanapi/wallet/api', () => ({
  getReferralOverview: (...args: unknown[]) => getReferralOverview(...args),
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
        invite_count: 2,
        pending_bonus_quota: 100000,
        total_bonus_quota: 300000,
        reward_rate: 0.05,
        freeze_days: 7,
      },
    })
  })

  test('uses the compact native referral card with link and summary stats', async () => {
    renderPage()

    expect(
      (await screen.findAllByText('Referral Program')).length
    ).toBeGreaterThan(0)
    expect(
      screen.getByDisplayValue('https://example.test/sign-up?aff=demo')
    ).toBeInTheDocument()
    expect(await screen.findByText(/5%/)).toBeInTheDocument()
    expect(screen.getByText('2')).toBeInTheDocument()
    expect(screen.getByText('Invites')).toBeInTheDocument()
  })
})
