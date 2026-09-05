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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { getSelf } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import {
  calculateAmount,
  getTopupInfo,
  getWalletSummary,
  redeemTopupCode,
  requestPayment,
} from '../api'
import { Wallet } from '../index'
import type { TopupInfo, UserWalletData, WalletSummary } from '../types'

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({
    status: { price: 7.3 },
    loading: false,
    error: null,
  }),
}))

vi.mock('@/hooks/use-system-config', () => ({
  useSystemConfig: () => ({
    currency: { quotaDisplayType: 'USD', usdExchangeRate: 1 },
    loading: false,
    logoLoaded: true,
  }),
}))

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  getSelf: vi.fn(),
}))

vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  getTopupInfo: vi.fn(),
  calculateAmount: vi.fn(),
  requestPayment: vi.fn(),
  redeemTopupCode: vi.fn(),
  getWalletSummary: vi.fn(),
}))

vi.mock('../components/ledger-table', () => ({
  LedgerTable: () => null,
}))

const walletUser: UserWalletData = {
  id: 7,
  username: 'wallet-user',
  quota: 1_000_000,
  used_quota: 250_000,
  request_count: 42,
  aff_count: 0,
  group: 'default',
}

const walletSummary: WalletSummary = {
  total: 1_000_000,
  paid: 800_000,
  bonus: 200_000,
  rules: {
    signup_bonus_enabled: false,
    signup_bonus_quota: 0,
    referral_bonus_enabled: false,
    referral_bonus_rate: 0,
    referral_bonus_freeze_days: 0,
  },
}

const onlineTopupInfo: TopupInfo = {
  enable_online_topup: true,
  enable_stripe_topup: false,
  enable_redemption: true,
  pay_methods: [
    { name: 'Epay', type: 'epay', min_topup: 10 },
    { name: 'Alipay', type: 'alipay', min_topup: 10 },
  ],
  min_topup: 10,
  stripe_min_topup: 5,
  amount_options: [10, 50, 100],
  discount: {},
}

describe('wallet funding flow', () => {
  beforeEach(() => {
    useAuthStore
      .getState()
      .auth.setUser({ id: walletUser.id, username: 'wallet-user', role: 1 })
    vi.mocked(getSelf).mockResolvedValue({
      success: true,
      data: { ...walletUser, username: 'wallet-user', role: 1 },
    })
    vi.mocked(getWalletSummary).mockResolvedValue({
      success: true,
      data: walletSummary,
    })
    vi.mocked(calculateAmount).mockResolvedValue({ success: true, data: '73' })
  })

  test('hides payment methods and the redemption input when topup info fails to load', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: false,
      message: 'topup unavailable',
    })

    render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Wallet />
      </QueryClientProvider>
    )

    expect(
      await screen.findByText(
        'Online topup is not enabled. Please use redemption code or contact administrator.'
      )
    ).toBeInTheDocument()

    expect(
      screen.queryByRole('button', { name: 'Epay' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Alipay' })
    ).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Custom Amount')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Have a Code?')).not.toBeInTheDocument()
    expect(
      screen.getByText(
        'Redemption codes are disabled until the administrator confirms compliance terms.'
      )
    ).toBeInTheDocument()
  })

  test('shows the account failure with retry instead of zero balances and recovers after reload', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })
    vi.mocked(getSelf).mockRejectedValueOnce(new Error('account down'))

    render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Wallet />
      </QueryClientProvider>
    )

    const alert = await screen.findByRole('alert')
    expect(
      within(alert).getByRole('button', { name: 'Retry' })
    ).toBeInTheDocument()

    await userEvent
      .setup()
      .click(within(alert).getByRole('button', { name: 'Retry' }))

    await waitFor(() =>
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    )
    expect(await screen.findByLabelText('Custom Amount')).toBeInTheDocument()
  })

  test('shows dash for usage and requests while the account request fails and restores real numbers after retry', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })
    vi.mocked(getSelf).mockRejectedValueOnce(new Error('account down'))

    const { container } = render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Wallet />
      </QueryClientProvider>
    )

    const alert = await screen.findByRole('alert')
    const usageValue = () =>
      container.querySelector(
        '[data-wallet-stat="usage"] [data-wallet-stat-value]'
      )
    const requestsValue = () =>
      container.querySelector(
        '[data-wallet-stat="requests"] [data-wallet-stat-value]'
      )
    expect(usageValue()?.textContent).toBe('-')
    expect(requestsValue()?.textContent).toBe('-')

    await userEvent
      .setup()
      .click(within(alert).getByRole('button', { name: 'Retry' }))

    await waitFor(() =>
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    )
    await waitFor(() => expect(usageValue()?.textContent).toBe('$0.50'))
    expect(requestsValue()?.textContent).toBe('42')
  })

  test('shows dash for paid and bonus while the summary request fails and restores real amounts after retry', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })
    vi.mocked(getWalletSummary).mockRejectedValueOnce(new Error('summary down'))

    const { container } = render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Wallet />
      </QueryClientProvider>
    )

    const alert = await screen.findByRole('alert')
    const paidValue = () =>
      container.querySelector(
        '[data-wallet-stat="paid"] [data-wallet-stat-value]'
      )
    const bonusValue = () =>
      container.querySelector(
        '[data-wallet-stat="bonus"] [data-wallet-stat-value]'
      )
    expect(paidValue()?.textContent).toBe('-')
    expect(bonusValue()?.textContent).toBe('-')

    await userEvent
      .setup()
      .click(within(alert).getByRole('button', { name: 'Retry' }))

    await waitFor(() =>
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    )
    await waitFor(() => expect(paidValue()?.textContent).toBe('$1.60'))
    expect(bonusValue()?.textContent).toBe('$0.40')
  })

  test('shows amount, payment methods, and redemption input when online topup and redemption are enabled', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })

    render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Wallet />
      </QueryClientProvider>
    )

    const amountInput = await screen.findByLabelText('Custom Amount')
    await waitFor(() => expect(amountInput).toHaveDisplayValue('10'))

    expect(screen.getByLabelText('Have a Code?')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Redeem' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Epay' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Alipay' })).toBeEnabled()
    expect(screen.getByRole('button', { name: /^50 Pay/ })).toBeInTheDocument()

    await waitFor(() =>
      expect(calculateAmount).toHaveBeenCalledWith({ amount: 10 })
    )
  })

  test('opens the payment confirm dialog and submits the selected method with the entered amount', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })
    vi.mocked(requestPayment).mockResolvedValue({
      success: true,
      data: {},
      url: 'https://pay.example.com/submit',
    })
    // jsdom cannot submit real payment forms; the redirect target itself is
    // covered by the requestPayment assertion below.
    vi.spyOn(HTMLFormElement.prototype, 'submit').mockImplementation(() => {})

    render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Wallet />
      </QueryClientProvider>
    )

    const epayButton = await screen.findByRole('button', { name: 'Epay' })
    await waitFor(() => expect(epayButton).toBeEnabled())
    await userEvent.setup().click(epayButton)

    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText('Epay')).toBeInTheDocument()

    const summaryCallsBeforeConfirm =
      vi.mocked(getWalletSummary).mock.calls.length
    await userEvent
      .setup()
      .click(within(dialog).getByRole('button', { name: 'Confirm Payment' }))

    await waitFor(() => {
      expect(requestPayment).toHaveBeenCalledWith({
        amount: 10,
        payment_method: 'epay',
      })
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
      expect(vi.mocked(getWalletSummary).mock.calls.length).toBe(
        summaryCallsBeforeConfirm + 1
      )
    })
  })

  test('clears the redemption code and refreshes user and wallet summary after a successful redemption', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })
    vi.mocked(redeemTopupCode).mockResolvedValue({
      success: true,
      data: 500_000,
    })

    render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Wallet />
      </QueryClientProvider>
    )

    const codeInput = await screen.findByLabelText('Have a Code?')
    await userEvent.setup().type(codeInput, 'WELCOME-10')

    const selfCallsBeforeRedeem = vi.mocked(getSelf).mock.calls.length
    const summaryCallsBeforeRedeem =
      vi.mocked(getWalletSummary).mock.calls.length

    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Redeem' }))

    await waitFor(() => {
      expect(redeemTopupCode).toHaveBeenCalledWith({ key: 'WELCOME-10' })
      expect(codeInput).toHaveDisplayValue('')
      expect(vi.mocked(getSelf).mock.calls.length).toBeGreaterThan(
        selfCallsBeforeRedeem
      )
      expect(vi.mocked(getWalletSummary).mock.calls.length).toBe(
        summaryCallsBeforeRedeem + 1
      )
    })
  })

  test('refreshes balances when the wallet becomes visible again and removes listeners on unmount', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })
    const visibilityState = vi.spyOn(document, 'visibilityState', 'get')
    visibilityState.mockReturnValue('visible')

    const view = render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Wallet />
      </QueryClientProvider>
    )
    await screen.findByLabelText('Custom Amount')

    const initialSelfCalls = vi.mocked(getSelf).mock.calls.length
    const initialSummaryCalls = vi.mocked(getWalletSummary).mock.calls.length
    window.dispatchEvent(new Event('focus'))

    await waitFor(() => {
      expect(vi.mocked(getSelf).mock.calls.length).toBe(initialSelfCalls + 1)
      expect(vi.mocked(getWalletSummary).mock.calls.length).toBe(
        initialSummaryCalls + 1
      )
    })

    visibilityState.mockReturnValue('hidden')
    document.dispatchEvent(new Event('visibilitychange'))
    expect(vi.mocked(getSelf).mock.calls.length).toBe(initialSelfCalls + 1)

    visibilityState.mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    await waitFor(() => {
      expect(vi.mocked(getSelf).mock.calls.length).toBe(initialSelfCalls + 2)
      expect(vi.mocked(getWalletSummary).mock.calls.length).toBe(
        initialSummaryCalls + 2
      )
    })

    view.unmount()
    window.dispatchEvent(new Event('focus'))
    expect(vi.mocked(getSelf).mock.calls.length).toBe(initialSelfCalls + 2)
    visibilityState.mockRestore()
  })

  test('keeps the redemption input hidden when the backend does not enable redemption', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: { ...onlineTopupInfo, enable_redemption: false },
    })

    render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Wallet />
      </QueryClientProvider>
    )

    const amountInput = await screen.findByLabelText('Custom Amount')
    await waitFor(() => expect(amountInput).toHaveDisplayValue('10'))
    expect(screen.getByRole('button', { name: 'Epay' })).toBeEnabled()
    expect(screen.queryByLabelText('Have a Code?')).not.toBeInTheDocument()
    expect(
      screen.getByText(
        'Redemption codes are disabled until the administrator confirms compliance terms.'
      )
    ).toBeInTheDocument()
  })

  test('does not render referral rewards or referral history surfaces', async () => {
    vi.mocked(getTopupInfo).mockResolvedValue({
      success: true,
      data: onlineTopupInfo,
    })

    render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Wallet />
      </QueryClientProvider>
    )

    expect(await screen.findByLabelText('Custom Amount')).toBeInTheDocument()
    expect(screen.queryByText('Referral Rewards')).not.toBeInTheDocument()
    expect(screen.queryByText('Referral History')).not.toBeInTheDocument()
    expect(screen.queryByText('Referral link')).not.toBeInTheDocument()
  })
})
