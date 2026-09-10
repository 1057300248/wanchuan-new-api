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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

import { useRedemption } from '../hooks'

interface RedemptionCardProps {
  enabled: boolean
  onRedeemed: () => void
}

export function RedemptionCard(props: RedemptionCardProps) {
  const { t } = useTranslation()
  const [code, setCode] = useState('')
  const { redeeming, redeemCode } = useRedemption()
  if (!props.enabled) return null

  const handleRedeem = async () => {
    if (await redeemCode(code)) {
      setCode('')
      props.onRedeemed()
    }
  }

  return (
    <Card data-card-hover='false' className='bg-muted/20 py-0'>
      <CardHeader>
        <CardTitle>{t('Redeem Code')}</CardTitle>
      </CardHeader>
      <CardContent className='flex gap-2 pb-4'>
        <Input
          value={code}
          onChange={(event) => setCode(event.currentTarget.value)}
          placeholder={t('Enter redemption code')}
          aria-label={t('Redemption code')}
        />
        <Button onClick={handleRedeem} disabled={redeeming || !code.trim()}>
          {redeeming ? t('Redeeming') : t('Redeem')}
        </Button>
      </CardContent>
    </Card>
  )
}
