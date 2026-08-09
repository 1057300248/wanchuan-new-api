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
import { after, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLAnchorElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { mock } = await import('bun:test')

function MockLink(props: {
  to?: string
  params?: Record<string, string>
  className?: string
  children?: React.ReactNode
}) {
  const href = props.params
    ? props.to?.replaceAll(/\$(\w+)/g, (_match, key) => props.params?.[key] ?? '')
    : props.to

  return (
    <a href={href} className={props.className}>
      {props.children}
    </a>
  )
}

mock.module('@tanstack/react-router', () => ({ Link: MockLink }))

const walletState: {
  summary: unknown
  overview: unknown
  walletLoading: boolean
  referralLoading: boolean
} = {
  summary: null,
  overview: null,
  walletLoading: true,
  referralLoading: true,
}

mock.module('@/features/wallet/hooks/use-wallet', () => ({
  useWalletSummary: () => ({
    summary: walletState.summary,
    loading: walletState.walletLoading,
    refetch: async () => {},
  }),
  useReferralOverview: () => ({
    overview: walletState.overview,
    loading: walletState.referralLoading,
    submitting: false,
    submitWithdrawal: async () => false,
    refetch: async () => {},
  }),
}))

const { FinancialSummaryCard } = await import('../financial-summary-card')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Financial Summary': 'Financial Summary',
        'Account balances and referral rewards':
          'Account balances and referral rewards',
        'View wallet': 'View wallet',
        'Wallet summary unavailable': 'Wallet summary unavailable',
        'Could not load wallet data': 'Could not load wallet data',
        Retry: 'Retry',
        'Total Balance': 'Total Balance',
        'Paid Balance': 'Paid Balance',
        'Bonus Balance': 'Bonus Balance',
        'Affiliate Balance': 'Affiliate Balance',
        'Sum of all wallet accounts': 'Sum of all wallet accounts',
        'From top-ups and transfers': 'From top-ups and transfers',
        'Signup bonus and invitee gifts': 'Signup bonus and invitee gifts',
        'Matured referral rewards': 'Matured referral rewards',
        'Referral Program': 'Referral Program',
        'Reward rate: {{rate}}%': 'Reward rate: {{rate}}%',
        'Unlock period: {{days}} days': 'Unlock period: {{days}} days',
        Pending: 'Pending',
        Available: 'Available',
        'Invited users': 'Invited users',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const RULES = {
  signup_bonus_enabled: true,
  signup_bonus_quota: 300000,
  affiliate_reward_enabled: true,
  affiliate_reward_rate: 0.2,
  affiliate_freeze_days: 7,
  affiliate_min_withdraw_quota: 100000,
}

function setWalletState(
  next: Partial<typeof walletState>
): void {
  Object.assign(walletState, next)
}

describe('FinancialSummaryCard', () => {
  after(() => {
    domWindow.close()
  })

  test('renders skeleton placeholders while the wallet and referral hooks are loading', async () => {
    setWalletState({ walletLoading: true, referralLoading: true })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <FinancialSummaryCard />
        </I18nextProvider>
      )
    )

    const card = container.querySelector('[data-financial-summary-card]')
    assert.ok(card)
    assert.equal(
      card.getAttribute('data-financial-summary-state'),
      'loading'
    )
    assert.equal(container.querySelectorAll('[data-financial-summary-account]').length, 0)
    assert.ok(container.querySelector('[data-slot="skeleton"]'))
    assert.equal(container.textContent?.includes('Retry'), false)

    await act(async () => root.unmount())
    container.remove()
  })

  test('degrades to a compact unavailable card with retry when both sources fail', async () => {
    setWalletState({
      summary: null,
      overview: null,
      walletLoading: false,
      referralLoading: false,
    })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <FinancialSummaryCard />
        </I18nextProvider>
      )
    )

    const card = container.querySelector('[data-financial-summary-card]')
    assert.ok(card)
    assert.equal(card.getAttribute('data-financial-summary-state'), 'failed')
    assert.equal(container.textContent?.includes('Wallet summary unavailable'), true)
    assert.equal(container.textContent?.includes('Could not load wallet data'), true)

    const retry = container.querySelector<HTMLButtonElement>('button')
    assert.ok(retry)
    assert.equal(retry.textContent, 'Retry')

    await act(async () => root.unmount())
    container.remove()
  })

  test('renders the four account balances and referral summary when data is ready', async () => {
    setWalletState({
      summary: {
        total: 1500000,
        paid: 1000000,
        bonus: 300000,
        affiliate: 200000,
        rules: RULES,
      },
      overview: {
        aff_code: 'abc123',
        inviter_id: 0,
        invite_count: 3,
        pending_count: 1,
        pending_quota: 50000,
        available_quota: 100000,
        withdrawn_quota: 20000,
        total_earned_quota: 170000,
        rules: RULES,
      },
      walletLoading: false,
      referralLoading: false,
    })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <FinancialSummaryCard />
        </I18nextProvider>
      )
    )

    const card = container.querySelector('[data-financial-summary-card]')
    assert.ok(card)
    assert.equal(card.getAttribute('data-financial-summary-state'), 'ready')

    const accounts = container.querySelectorAll('[data-financial-summary-account]')
    assert.equal(accounts.length, 4)
    assert.deepEqual(
      [...accounts].map((node) => node.getAttribute('data-financial-summary-account')),
      ['total', 'paid', 'bonus', 'affiliate']
    )
    assert.equal(container.textContent?.includes('$3'), true)
    assert.equal(container.textContent?.includes('$2'), true)
    assert.equal(container.textContent?.includes('$0.6'), true)
    assert.equal(container.textContent?.includes('$0.4'), true)

    const referral = container.querySelector('[data-financial-summary="referral"]')
    assert.ok(referral)
    assert.equal(container.textContent?.includes('Referral Program'), true)
    assert.equal(container.textContent?.includes('Invited users'), true)
    assert.equal(container.textContent?.includes('Reward rate: 20%'), true)

    const stats = referral.querySelectorAll('[data-financial-summary="referral-stats"] div')
    assert.ok(stats.length >= 3)

    const walletLink = container.querySelector<HTMLAnchorElement>('a')
    assert.ok(walletLink)
    assert.equal(walletLink.getAttribute('href'), '/wallet')

    await act(async () => root.unmount())
    container.remove()
  })
})
