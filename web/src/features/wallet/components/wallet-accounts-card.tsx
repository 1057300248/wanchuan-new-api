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
import { CircleDollarSign, Gift, HandCoins, WalletCards } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota } from '@/lib/format'

import type { WalletSummary } from '../types'

interface WalletAccountsCardProps {
  summary: WalletSummary | null
  loading?: boolean
}

export function WalletAccountsCard(props: WalletAccountsCardProps) {
  const { t } = useTranslation()

  if (props.loading) {
    return (
      <div className='grid grid-cols-2 gap-3 lg:grid-cols-4'>
        {['total', 'paid', 'bonus', 'affiliate'].map((key) => (
          <div key={key} className='rounded-lg border p-3 sm:p-4'>
            <Skeleton className='h-3.5 w-20' />
            <Skeleton className='mt-2 h-6 w-full' />
            <Skeleton className='mt-1.5 h-3 w-24' />
          </div>
        ))}
      </div>
    )
  }

  const cards: {
    label: string
    value: number
    description: string
    icon: typeof WalletCards
    tone: IconBadgeTone
  }[] = [
    {
      label: t('Total Balance'),
      value: props.summary?.total ?? 0,
      description: t('Sum of all wallet accounts'),
      icon: WalletCards,
      tone: 'success',
    },
    {
      label: t('Paid Balance'),
      value: props.summary?.paid ?? 0,
      description: t('From top-ups and transfers'),
      icon: CircleDollarSign,
      tone: 'info',
    },
    {
      label: t('Bonus Balance'),
      value: props.summary?.bonus ?? 0,
      description: t('Signup bonus and invitee gifts'),
      icon: Gift,
      tone: 'chart-2',
    },
    {
      label: t('Affiliate Balance'),
      value: props.summary?.affiliate ?? 0,
      description: t('Matured referral rewards'),
      icon: HandCoins,
      tone: 'chart-3',
    },
  ]

  return (
    <div className='grid grid-cols-2 gap-3 lg:grid-cols-4'>
      {cards.map((item) => (
        <div key={item.label} className='rounded-lg border p-3 sm:p-4'>
          <div className='flex items-center gap-2'>
            <IconBadge tone={item.tone} size='sm'>
              <item.icon />
            </IconBadge>
            <div className='text-muted-foreground truncate text-[11px] font-medium tracking-wider uppercase sm:text-xs'>
              {item.label}
            </div>
          </div>
          <div className='text-foreground mt-2 font-mono text-base font-bold tracking-tight break-all tabular-nums sm:text-xl'>
            {formatQuota(item.value)}
          </div>
          <div className='text-muted-foreground/60 mt-0.5 hidden text-xs md:block'>
            {item.description}
          </div>
        </div>
      ))}
    </div>
  )
}
