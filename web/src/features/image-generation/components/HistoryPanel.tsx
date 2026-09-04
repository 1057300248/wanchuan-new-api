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
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import { downloadImage } from '../api'
import type { ImageHistoryEntry } from '../types'

type HistoryPanelProps = {
  entries: ImageHistoryEntry[]
  totalCount: number
  query: string
  onQueryChange: (value: string) => void
  orientation: 'all' | 'square' | 'landscape' | 'portrait'
  onOrientationChange: (
    value: 'all' | 'square' | 'landscape' | 'portrait'
  ) => void
  onRemove: (id: string) => void
  onClear: () => void
  onKeepLocally: (entryId: string, resultId: string) => Promise<void>
  resolveUrl: (resultId: string, fallback: string) => string
}

export function HistoryPanel(props: HistoryPanelProps) {
  const { t } = useTranslation()
  const orientationLabels = {
    all: t('All orientations'),
    square: t('Square'),
    landscape: t('Landscape'),
    portrait: t('Portrait'),
  }

  const handleDownload = async (url: string, id: string) => {
    try {
      await downloadImage(url, `history-${id}.png`)
    } catch (error) {
      if (error instanceof Error && error.message === 'Image not available') {
        toast.error(t('Image not available'))
        return
      }
      toast.error(t('Download failed'))
    }
  }

  const handleKeep = async (entryId: string, resultId: string) => {
    try {
      await props.onKeepLocally(entryId, resultId)
      toast.success(t('Kept locally'))
    } catch {
      toast.error(t('Failed to keep image locally'))
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('History')}</CardTitle>
      </CardHeader>
      <CardContent className='grid gap-3'>
        <div className='grid gap-2 sm:grid-cols-2'>
          <div className='grid gap-1.5'>
            <Label htmlFor='image-history-search'>{t('Search history')}</Label>
            <Input
              id='image-history-search'
              value={props.query}
              placeholder={t('Search by prompt or model')}
              onChange={(event) => props.onQueryChange(event.target.value)}
            />
          </div>
          <div className='grid gap-1.5'>
            <Label htmlFor='image-history-orientation'>
              {t('Orientation')}
            </Label>
            <Select
              value={props.orientation}
              onValueChange={(next) => {
                if (next !== null) props.onOrientationChange(next)
              }}
            >
              <SelectTrigger id='image-history-orientation' className='w-full'>
                <SelectValue>
                  {orientationLabels[props.orientation]}
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value='all'>{t('All orientations')}</SelectItem>
                  <SelectItem value='square'>{t('Square')}</SelectItem>
                  <SelectItem value='landscape'>{t('Landscape')}</SelectItem>
                  <SelectItem value='portrait'>{t('Portrait')}</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
        </div>

        {props.totalCount === 0 ? (
          <p className='text-muted-foreground text-sm'>
            {t('No history yet.')}
          </p>
        ) : (
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={props.onClear}
          >
            {t('Clear history')}
          </Button>
        )}

        {props.totalCount > 0 && props.entries.length === 0 ? (
          <p className='text-muted-foreground text-sm'>
            {t('No matching history.')}
          </p>
        ) : null}

        <ul className='grid gap-3'>
          {props.entries.map((entry) => (
            <li key={entry.id} className='rounded-lg border p-2'>
              <p className='truncate text-sm font-medium' title={entry.prompt}>
                {entry.prompt}
              </p>
              <p className='text-muted-foreground text-xs'>
                {entry.model} · {entry.settings.size || t('Default size')}
              </p>
              <ul className='mt-2 grid grid-cols-3 gap-1.5'>
                {entry.results.map((result) => {
                  const url = props.resolveUrl(result.id, result.source)
                  return (
                    <li
                      key={result.id}
                      className='overflow-hidden rounded-md border'
                    >
                      <img
                        src={url}
                        alt={entry.prompt}
                        loading='lazy'
                        className='aspect-square w-full object-cover'
                      />
                      <div className='flex flex-wrap gap-1 p-1'>
                        <Button
                          type='button'
                          variant='ghost'
                          size='xs'
                          onClick={() => void handleDownload(url, result.id)}
                        >
                          {t('Download')}
                        </Button>
                        {!result.keptLocally ? (
                          <Button
                            type='button'
                            variant='ghost'
                            size='xs'
                            onClick={() => void handleKeep(entry.id, result.id)}
                          >
                            {t('Keep locally')}
                          </Button>
                        ) : null}
                      </div>
                    </li>
                  )
                })}
              </ul>
              <div className='mt-2'>
                <Button
                  type='button'
                  variant='ghost'
                  size='xs'
                  onClick={() => props.onRemove(entry.id)}
                  aria-label={t('Delete history {{prompt}}', {
                    prompt: entry.prompt,
                  })}
                >
                  {t('Delete')}
                </Button>
              </div>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
