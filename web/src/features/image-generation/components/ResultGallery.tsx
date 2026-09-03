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
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'

import { downloadImage } from '../api'
import type { GeneratedImage } from '../types'

type ResultGalleryProps = {
  results: GeneratedImage[]
  isGenerating: boolean
  hasAttempted: boolean
}

export function ResultGallery(props: ResultGalleryProps) {
  const { t } = useTranslation()
  const [previewUrl, setPreviewUrl] = useState<string | null>(null)
  const [previewAlt, setPreviewAlt] = useState('')

  const handleDownload = async (image: GeneratedImage) => {
    try {
      await downloadImage(image.url, `image-${image.id}.png`)
    } catch {
      toast.error(t('Download failed'))
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Results')}</CardTitle>
      </CardHeader>
      <CardContent>
        {props.isGenerating ? (
          <div aria-live='polite' className='grid grid-cols-2 gap-2'>
            <Skeleton className='aspect-square w-full' />
            <Skeleton className='aspect-square w-full' />
            <span className='sr-only'>{t('Generating images...')}</span>
          </div>
        ) : null}
        {!props.isGenerating && props.results.length === 0 ? (
          <p className='text-muted-foreground text-sm'>
            {props.hasAttempted
              ? t('No images were returned. Try adjusting your prompt.')
              : t('Generated images will appear here.')}
          </p>
        ) : null}
        {props.results.length > 0 ? (
          <ul className='grid grid-cols-2 gap-2'>
            {props.results.map((image) => (
              <li key={image.id} className='overflow-hidden rounded-lg border'>
                <button
                  type='button'
                  className='block w-full'
                  onClick={() => {
                    setPreviewUrl(image.url)
                    setPreviewAlt(image.prompt)
                  }}
                  aria-label={t('Preview image for {{prompt}}', {
                    prompt: image.prompt,
                  })}
                >
                  <img
                    src={image.url}
                    alt={image.prompt}
                    loading='lazy'
                    className='aspect-square w-full object-cover'
                  />
                </button>
                <div className='flex gap-1 p-1.5'>
                  <Button
                    type='button'
                    variant='outline'
                    size='xs'
                    onClick={() => {
                      setPreviewUrl(image.url)
                      setPreviewAlt(image.prompt)
                    }}
                  >
                    {t('Preview')}
                  </Button>
                  <Button
                    type='button'
                    variant='outline'
                    size='xs'
                    onClick={() => void handleDownload(image)}
                  >
                    {t('Download')}
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        ) : null}
        <Dialog
          open={previewUrl !== null}
          onOpenChange={(open) => !open && setPreviewUrl(null)}
        >
          <DialogContent className='max-w-3xl'>
            <DialogTitle className='sr-only'>
              {previewAlt || t('Image preview')}
            </DialogTitle>
            {previewUrl ? (
              <img
                src={previewUrl}
                alt={previewAlt}
                className='max-h-[70vh] w-full object-contain'
              />
            ) : null}
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>
  )
}
