/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { createFileRoute } from '@tanstack/react-router'
import { useEffect } from 'react'

import { Wallet } from '@/features/wallet'

export const Route = createFileRoute('/_authenticated/dashboard/referrals/')({
  component: ReferralWallet,
})

function ReferralWallet() {
  useEffect(() => {
    const timer = window.setTimeout(() => {
      document.querySelector('#referral')?.scrollIntoView({ block: 'start' })
    }, 0)
    return () => window.clearTimeout(timer)
  }, [])
  return <Wallet />
}
