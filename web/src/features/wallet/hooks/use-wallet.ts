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
import i18next from 'i18next'
import { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'

import {
  createReferralWithdrawal,
  getReferralOverview,
  getWalletSummary,
} from '../api'
import type { ReferralOverview, WalletSummary } from '../types'

// ============================================================================
// Wallet / Referral Data Hook (TokenMix P1)
// ============================================================================

export function useWalletSummary() {
  const [summary, setSummary] = useState<WalletSummary | null>(null)
  const [loading, setLoading] = useState(true)

  const refetch = useCallback(async () => {
    try {
      setLoading(true)
      const response = await getWalletSummary()
      if (response.success && response.data) {
        setSummary(response.data)
      }
    } catch {
      // keep previous data; wallet page shows a fallback state
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    refetch()
  }, [refetch])

  return { summary, loading, refetch }
}

export function useReferralOverview() {
  const [overview, setOverview] = useState<ReferralOverview | null>(null)
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)

  const refetch = useCallback(async () => {
    try {
      setLoading(true)
      const response = await getReferralOverview()
      if (response.success && response.data) {
        setOverview(response.data)
      }
    } catch {
      // keep previous data
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    refetch()
  }, [refetch])

  const submitWithdrawal = useCallback(
    async (amount: number): Promise<boolean> => {
      try {
        setSubmitting(true)
        const response = await createReferralWithdrawal({
          amount,
          request_id: crypto.randomUUID(),
        })
        if (response.success) {
          toast.success(i18next.t('Withdrawal request submitted'))
          await refetch()
          return true
        }
        toast.error(response.message || i18next.t('Withdrawal request failed'))
        return false
      } catch {
        toast.error(i18next.t('Withdrawal request failed'))
        return false
      } finally {
        setSubmitting(false)
      }
    },
    [refetch]
  )

  return { overview, loading, submitting, submitWithdrawal, refetch }
}
