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
import {
  act,
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
} from '@testing-library/react'
import type { InternalAxiosRequestConfig } from 'axios'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { applyAuthBundle, clearAuthentication } from '@/lib/auth-session'
import type { AuthBundle } from '@/stores/auth-store'

import { PaymentConfirmDialog } from '../components/dialogs/payment-confirm-dialog'
import { usePayment } from '../hooks/use-payment'

const originalAdapter = api.defaults.adapter
const pending: Array<{
  config: InternalAxiosRequestConfig
  resolve: (price: string) => void
  reject: (error: Error) => void
}> = []
beforeEach(() => {
  pending.length = 0
  api.defaults.adapter = async (config) => {
    const price = await new Promise<string>((resolve, reject) =>
      pending.push({ config, resolve, reject })
    )
    return {
      data: { success: true, data: price },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
})
afterEach(() => {
  cleanup()
  clearAuthentication(false)
  api.defaults.adapter = originalAdapter
})

async function beginQuote(
  result: { current: ReturnType<typeof usePayment> },
  amount: number,
  method: string
) {
  const expected = pending.length + 1
  let promise!: Promise<number>
  act(() => {
    promise = result.current.calculatePaymentAmount(amount, method)
  })
  await waitFor(() => expect(pending).toHaveLength(expected))
  return { promise }
}

test('older responses cannot replace the latest amount quote or enable a mismatched checkout', async () => {
  const { result } = renderHook(() => usePayment())
  const first = await beginQuote(result, 100, 'alipay')
  const latest = await beginQuote(result, 200, 'alipay')
  await act(async () => {
    pending[1].resolve('1460.25')
    await latest.promise
  })
  expect(result.current.amount).toBe(1460.25)
  expect(result.current.isQuoteCurrent(200, 'alipay')).toBe(true)
  expect(result.current.isQuoteCurrent(100, 'alipay')).toBe(false)
  await act(async () => {
    pending[0].resolve('730')
    await first.promise
  })
  expect(result.current.amount).toBe(1460.25)
  expect(result.current.isQuoteCurrent(200, 'alipay')).toBe(true)
})

test('same amount with a different method invalidates the quote and older completion cannot clear the pending state', async () => {
  const { result } = renderHook(() => usePayment())
  const first = await beginQuote(result, 10, 'alipay')
  const latest = await beginQuote(result, 10, 'stripe')
  await act(async () => {
    pending[0].resolve('73')
    await first.promise
  })
  expect(result.current.calculating).toBe(true)
  expect(result.current.isQuoteCurrent(10, 'stripe')).toBe(false)
  expect(result.current.isQuoteCurrent(10, 'alipay')).toBe(false)
  await act(async () => {
    pending[1].resolve('0.00984')
    await latest.promise
  })
  expect(result.current.amount).toBe(0.00984)
  expect(result.current.calculating).toBe(false)
  expect(result.current.isQuoteCurrent(10, 'stripe')).toBe(true)
  expect(result.current.isQuoteCurrent(10, 'alipay')).toBe(false)
})

test('a stale error cannot clear a newer successful server quote', async () => {
  const { result } = renderHook(() => usePayment())
  const first = await beginQuote(result, 10, 'alipay')
  const latest = await beginQuote(result, 20, 'alipay')
  await act(async () => {
    pending[1].resolve('146.5')
    await latest.promise
  })
  await act(async () => {
    pending[0].reject(new Error('synthetic network failure'))
    await first.promise
  })
  expect(result.current.amount).toBe(146.5)
  expect(result.current.isQuoteCurrent(20, 'alipay')).toBe(true)
})

test.each(['not-a-number', 'Infinity', '0', '-1'])(
  'invalid quote %s cannot authorize checkout',
  async (price) => {
    const { result } = renderHook(() => usePayment())
    const quote = await beginQuote(result, 10, 'alipay')
    await act(async () => {
      pending[0].resolve(price)
      await quote.promise
    })
    expect(result.current.calculating).toBe(false)
    expect(result.current.isQuoteCurrent(10, 'alipay')).toBe(false)
  }
)

test('unmount invalidates an in-flight quote', async () => {
  const { result, unmount } = renderHook(() => usePayment())
  const quote = await beginQuote(result, 10, 'alipay')
  const isCurrent = result.current.isQuoteCurrent
  unmount()
  await act(async () => {
    pending[0].resolve('73')
    await quote.promise
  })
  expect(isCurrent(10, 'alipay')).toBe(false)
})

test('confirmation stays disabled while calculating or when quote identity is invalid', () => {
  const onConfirm = vi.fn()
  const props = {
    open: true,
    onOpenChange: vi.fn(),
    onConfirm,
    topupAmount: 10,
    paymentAmount: 0.00984,
    paymentMethod: undefined,
    calculating: true,
    processing: false,
    quoteValid: true,
  }
  const { rerender } = render(<PaymentConfirmDialog {...props} />)
  const button = screen.getByRole('button', { name: 'Confirm Payment' })
  expect(button).toBeDisabled()
  fireEvent.click(button)
  expect(onConfirm).not.toHaveBeenCalled()
  rerender(
    <PaymentConfirmDialog {...props} calculating={false} quoteValid={false} />
  )
  expect(button).toBeDisabled()
  fireEvent.click(button)
  expect(onConfirm).not.toHaveBeenCalled()
  rerender(<PaymentConfirmDialog {...props} calculating={false} />)
  expect(button).toBeEnabled()
  fireEvent.click(button)
  expect(onConfirm).toHaveBeenCalledTimes(1)
})

function signInQuoteOwner(id: number) {
  const now = Math.floor(Date.now() / 1000)
  const bundle: AuthBundle = {
    access_token: `synthetic-quote-access-${id}`,
    token_type: 'Bearer',
    access_expires_at: now + 600,
    user: { id, username: `synthetic-quote-user-${id}`, role: 1 },
    session: {
      sid: `synthetic-quote-session-${id}`,
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'test',
      created_at: now,
      last_active_at: now,
      expires_at: now + 3600,
    },
  }
  applyAuthBundle(bundle, false)
}

test.each([101, 202])('a completed quote cannot authorize owner %i after sign-out', async (nextOwner) => {
  signInQuoteOwner(101)
  const { result } = renderHook(() => usePayment())
  const quote = await beginQuote(result, 100, 'alipay')
  await act(async () => { pending[0].resolve('73.125'); await quote.promise })
  expect(result.current.isQuoteCurrent(100, 'alipay')).toBe(true)
  act(() => {
    clearAuthentication(false)
    signInQuoteOwner(nextOwner)
  })
  expect(result.current.isQuoteCurrent(100, 'alipay')).toBe(false)
})

test.each([101, 202])('a delayed prior-session quote cannot replace a quote for owner %i', async (nextOwner) => {
  signInQuoteOwner(101)
  const { result } = renderHook(() => usePayment())
  const previous = await beginQuote(result, 100, 'alipay')
  act(() => {
    clearAuthentication(false)
    signInQuoteOwner(nextOwner)
  })
  await act(async () => {
    pending[0].resolve('73.125')
    expect(await previous.promise).toBe(0)
  })
  expect(result.current.amount).toBe(0)
  expect(result.current.isQuoteCurrent(100, 'alipay')).toBe(false)
  const current = await beginQuote(result, 200, 'alipay')
  await act(async () => { pending[1].resolve('146.25'); await current.promise })
  expect(result.current.amount).toBe(146.25)
  expect(result.current.isQuoteCurrent(200, 'alipay')).toBe(true)
})
