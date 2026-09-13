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
import { Download } from 'lucide-react'
import { QRCodeCanvas, QRCodeSVG } from 'qrcode.react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { CHUANAPI_BRAND_LOGO } from '@/extensions/chuanapi/brand'

import { downloadReferralPoster } from '../lib/poster'

const POSTER_QR_SIZE = 360

export function ReferralPosterCard(props: { link: string; loading: boolean }) {
  const { t } = useTranslation()
  const qrExportRef = useRef<HTMLCanvasElement>(null)
  const [downloading, setDownloading] = useState(false)

  const handleDownload = async () => {
    if (!props.link || !qrExportRef.current || downloading) return
    setDownloading(true)
    try {
      await downloadReferralPoster({
        link: props.link,
        qrCanvas: qrExportRef.current,
        headline: t('Referral Program'),
        rewardLine: t('Share and earn 5% on the first three top-ups'),
        cta: t('Scan the QR code to sign up at {{link}}', { link: props.link }),
      })
    } catch {
      toast.error(t('Failed to download the poster. Please try again.'))
    } finally {
      setDownloading(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Referral Poster')}</CardTitle>
        <CardDescription>
          {t(
            'Use the fixed poster template with your QR code and referral link.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className='bg-muted/20 mx-auto flex max-w-sm flex-col items-center gap-3 rounded-xl border p-6 text-center'>
          <img
            src={CHUANAPI_BRAND_LOGO}
            alt=''
            aria-hidden='true'
            className='size-12'
          />
          <p className='text-lg font-semibold'>
            {t('Share and earn 5% on the first three top-ups')}
          </p>
          {props.link ? (
            <QRCodeSVG
              value={props.link}
              size={160}
              includeMargin
              aria-label={t('Referral link:')}
            />
          ) : (
            <Skeleton className='size-40' />
          )}
          <p className='text-muted-foreground font-mono text-xs break-all'>
            {props.link || '-'}
          </p>
          <Button
            type='button'
            variant='outline'
            disabled={!props.link || props.loading || downloading}
            onClick={() => void handleDownload()}
          >
            <Download aria-hidden='true' />
            {downloading ? t('Preparing poster...') : t('Download poster')}
          </Button>
          <QRCodeCanvas
            ref={qrExportRef}
            value={props.link || t('Referral link:')}
            size={POSTER_QR_SIZE}
            includeMargin
            className='hidden'
            aria-hidden='true'
          />
        </div>
      </CardContent>
    </Card>
  )
}
