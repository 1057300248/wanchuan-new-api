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
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { getSelf } from '@/lib/api'

import { LedgerTable } from './components/ledger-table'
import { RedemptionCard } from './components/redemption-card'
import { ReferralHistoryCard } from './components/referral-history-card'
import { ReferralRewardsCard } from './components/referral-rewards-card'
import { WalletAccountsCard } from './components/wallet-accounts-card'
import { WalletStatsCard } from './components/wallet-stats-card'
import {
  useAffiliate,
  useReferralOverview,
  useTopupInfo,
  useWalletSummary,
} from './hooks'
import type { UserWalletData } from './types'

interface WalletProps {
  initialShowHistory?: boolean
}

export function Wallet(_props: WalletProps) {
  const { t } = useTranslation()
  const [user, setUser] = useState<UserWalletData | null>(null)
  const [userLoading, setUserLoading] = useState(true)
  const { topupInfo } = useTopupInfo()
  const { affiliateLink } = useAffiliate()
  const {
    summary,
    loading: walletLoading,
    refetch: refetchWallet,
  } = useWalletSummary()
  const {
    overview,
    loading: referralLoading,
    refetch: refetchReferral,
  } = useReferralOverview()

  const fetchUser = useCallback(async () => {
    try {
      setUserLoading(true)
      const response = await getSelf()
      if (response.success && response.data)
        setUser(response.data as UserWalletData)
    } finally {
      setUserLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchUser()
  }, [fetchUser])

  const refreshBalances = () => {
    void Promise.all([fetchUser(), refetchWallet(), refetchReferral()])
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Wallet')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto flex w-full max-w-7xl flex-col gap-4 sm:gap-5'>
          <WalletStatsCard user={user} loading={userLoading} />
          <WalletAccountsCard summary={summary} loading={walletLoading} />
          <RedemptionCard
            enabled={topupInfo?.enable_redemption === true}
            onRedeemed={refreshBalances}
          />
          <ReferralRewardsCard
            overview={overview}
            referralLink={affiliateLink}
            loading={referralLoading}
          />
          <ReferralHistoryCard />
          <LedgerTable />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
