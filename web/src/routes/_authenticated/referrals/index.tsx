import { createFileRoute } from '@tanstack/react-router'

import { Referrals } from '@/features/referrals'

export const Route = createFileRoute('/_authenticated/referrals/')({
  component: Referrals,
})
