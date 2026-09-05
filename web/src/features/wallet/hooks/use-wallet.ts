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
import { useCallback, useEffect, useState } from 'react'

import { getReferralOverview, getWalletSummary } from '../api'
import type { ReferralOverview, WalletSummary } from '../types'

// ============================================================================
// Wallet / Referral Data Hook (TokenMix P1)
// ============================================================================

export function useWalletSummary() {
  const [summary, setSummary] = useState<WalletSummary | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)

  const refetch = useCallback(async () => {
    try {
      setLoading(true)
      setError(false)
      const response = await getWalletSummary()
      if (response.success && response.data) {
        setSummary(response.data)
      } else {
        setError(true)
      }
    } catch {
      setError(true)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    refetch()
  }, [refetch])

  return { summary, loading, error, refetch }
}

export function useReferralOverview() {
  const [overview, setOverview] = useState<ReferralOverview | null>(null)
  const [loading, setLoading] = useState(true)

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

  return { overview, loading, refetch }
}
