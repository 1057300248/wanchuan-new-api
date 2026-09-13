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
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

function formatRate(rate: number): string {
  if (!Number.isFinite(rate) || rate <= 0) return '0%'
  const percent = rate * 100
  const text = Number.isInteger(percent)
    ? percent.toFixed(0)
    : percent.toFixed(2).replace(/0+$/, '').replace(/\.$/, '')
  return `${text}%`
}

export function PromoCopyPanel(props: { link: string; rate: number }) {
  const { t } = useTranslation()
  const rate = formatRate(props.rate)
  const link = props.link || '-'
  const copies = [
    t(
      'Join me on this AI platform and get {{rate}} back on each of your first three top-ups. There are no referral rewards from the fourth top-up on. Sign up here: {{link}}',
      { rate, link }
    ),
    t(
      'Refer a friend and earn {{rate}} of each of their first three top-ups; the fourth and later top-ups earn nothing. My link: {{link}}',
      { rate, link }
    ),
    t(
      'Top up through my link {{link}}: the first three top-ups each earn {{rate}} back, and the fourth top-up onwards has no rewards.',
      { rate, link }
    ),
  ]

  return (
    <div className='grid gap-3 lg:grid-cols-3'>
      {copies.map((copy, index) => (
        <Card key={copy} size='sm'>
          <CardHeader>
            <CardTitle className='text-sm'>
              {t('Promotion copy {{index}}', { index: index + 1 })}
            </CardTitle>
          </CardHeader>
          <CardContent className='flex flex-col gap-3'>
            <p className='text-muted-foreground text-sm leading-6'>{copy}</p>
            <CopyButton
              value={copy}
              variant='outline'
              size='sm'
              tooltip={t('Copy promotion copy')}
              className='self-start'
            >
              {t('Copy')}
            </CopyButton>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}
