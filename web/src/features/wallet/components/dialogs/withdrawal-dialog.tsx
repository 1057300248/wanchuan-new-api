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
import { Loader2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  formatQuota,
  parseQuotaFromDollars,
  quotaUnitsToDollars,
} from '@/lib/format'

interface WithdrawalDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: (quota: number) => Promise<boolean>
  availableQuota: number
  minWithdrawQuota: number
  submitting?: boolean
}

export function WithdrawalDialog(props: WithdrawalDialogProps) {
  const { t } = useTranslation()
  const minWithdrawDollars = quotaUnitsToDollars(props.minWithdrawQuota)
  const maxDollars = quotaUnitsToDollars(props.availableQuota)
  const [amount, setAmount] = useState(minWithdrawDollars)

  useEffect(() => {
    if (props.open) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setAmount(minWithdrawDollars)
    }
  }, [minWithdrawDollars, props.open])

  const quota = parseQuotaFromDollars(amount)
  const canSubmit =
    Number.isFinite(amount) &&
    quota >= props.minWithdrawQuota &&
    quota <= props.availableQuota

  const handleConfirm = async () => {
    if (!canSubmit) return
    const success = await props.onConfirm(quota)
    if (success) {
      props.onOpenChange(false)
    }
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Withdraw Rewards')}
      description={t(
        'Withdrawal is reviewed manually by the administrator. Funds are frozen once submitted.'
      )}
      contentClassName='max-sm:w-[calc(100vw-1.5rem)] sm:max-w-md'
      titleClassName='text-xl font-semibold'
      footerClassName='grid grid-cols-2 gap-2 sm:flex'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={props.submitting}
          >
            {t('Cancel')}
          </Button>
          <Button
            onClick={handleConfirm}
            disabled={props.submitting || !canSubmit}
          >
            {props.submitting && (
              <Loader2 className='mr-2 h-4 w-4 animate-spin' />
            )}
            {t('Submit Request')}
          </Button>
        </>
      }
    >
      <div className='space-y-4 py-3 sm:space-y-6 sm:py-4'>
        <div className='space-y-2'>
          <Label className='text-muted-foreground text-xs font-medium tracking-wider uppercase'>
            {t('Available Rewards')}
          </Label>
          <div className='text-2xl font-semibold'>
            {formatQuota(props.availableQuota)}
          </div>
        </div>

        <div className='space-y-3'>
          <Label
            htmlFor='withdraw-amount'
            className='text-muted-foreground text-xs font-medium tracking-wider uppercase'
          >
            {t('Withdrawal Amount')}
          </Label>
          <Input
            id='withdraw-amount'
            type='number'
            value={amount}
            onChange={(event) => setAmount(Number(event.target.value))}
            min={minWithdrawDollars}
            max={maxDollars}
            step={1}
            className='font-mono text-lg'
          />
          <p className='text-muted-foreground text-xs'>
            {t('Minimum:')} {formatQuota(props.minWithdrawQuota)}
          </p>
        </div>
      </div>
    </Dialog>
  )
}
