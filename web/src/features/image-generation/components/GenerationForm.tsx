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

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'

import {
  IMAGE_COUNT_MAX,
  IMAGE_COUNT_MIN,
  IMAGE_PROMPT_EXAMPLE_KEYS,
} from '../constants'
import type { ImageApiKeyOption, ReferenceImageItem } from '../types'
import { ReferenceUploader } from './ReferenceUploader'

const IMAGE_PARAMETER_LABEL_KEYS: Record<string, string> = {
  auto: 'Auto',
  low: 'Low',
  medium: 'Medium',
  high: 'High',
  standard: 'Standard',
  hd: 'HD',
  vivid: 'Vivid',
  natural: 'Natural',
}

export type GenerationFormValue = {
  prompt: string
  apiKeyId: number | null
  model: string
  size: string
  quality: string
  style: string
  count: number
}

type GenerationFormProps = {
  value: GenerationFormValue
  onChange: (patch: Partial<GenerationFormValue>) => void
  keys: ImageApiKeyOption[]
  models: string[]
  sizes: string[]
  qualities: string[]
  styles: string[]
  maxCount: number
  maxReferenceImages: number
  references: ReferenceImageItem[]
  referenceErrorKey: string | null
  onAddReferenceFiles: (files: File[]) => void
  onRemoveReference: (id: string) => void
  isGenerating: boolean
  formError: string | null
  isInitializing: boolean
  onGenerate: () => void
  onCancel: () => void
}

export function GenerationForm(props: GenerationFormProps) {
  const { t } = useTranslation()
  const promptInvalid = props.value.prompt.trim() === ''
  const countLimit = Math.max(
    IMAGE_COUNT_MIN,
    Math.min(IMAGE_COUNT_MAX, props.maxCount)
  )
  const selectedApiKeyName = props.keys.find(
    (key) => key.id === props.value.apiKeyId
  )?.name
  const canGenerate =
    !props.isGenerating &&
    !props.isInitializing &&
    !promptInvalid &&
    props.value.apiKeyId !== null &&
    props.value.model !== ''

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Create image')}</CardTitle>
      </CardHeader>
      <CardContent className='grid gap-4'>
        <div className='grid gap-2'>
          <Label htmlFor='image-prompt'>{t('Prompt')}</Label>
          <Textarea
            id='image-prompt'
            value={props.value.prompt}
            aria-invalid={promptInvalid}
            disabled={props.isGenerating}
            placeholder={t('Describe the image you want to create')}
            onChange={(event) => props.onChange({ prompt: event.target.value })}
            rows={4}
          />
          <div className='flex flex-wrap gap-2'>
            {IMAGE_PROMPT_EXAMPLE_KEYS.map((example) => (
              <Button
                key={example}
                type='button'
                variant='outline'
                size='xs'
                disabled={props.isGenerating}
                onClick={() => props.onChange({ prompt: example })}
              >
                {t(example)}
              </Button>
            ))}
          </div>
        </div>

        <div className='grid gap-2'>
          <Label htmlFor='image-api-key'>{t('API key')}</Label>
          <Select
            value={
              props.value.apiKeyId === null ? '' : String(props.value.apiKeyId)
            }
            onValueChange={(next) => {
              if (next !== null) {
                props.onChange({ apiKeyId: next === '' ? null : Number(next) })
              }
            }}
            disabled={props.isGenerating}
          >
            <SelectTrigger id='image-api-key' className='w-full'>
              <SelectValue placeholder={t('Select an API key')}>
                {selectedApiKeyName}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {props.keys.map((key) => (
                  <SelectItem key={key.id} value={String(key.id)}>
                    {key.name}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>

        <div className='grid gap-2'>
          <Label htmlFor='image-model'>{t('Model')}</Label>
          <Select
            value={props.value.model}
            onValueChange={(next) => {
              if (next !== null) props.onChange({ model: next })
            }}
            disabled={props.isGenerating || props.isInitializing}
          >
            <SelectTrigger id='image-model' className='w-full'>
              <SelectValue placeholder={t('Select a model')} />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {props.models.map((model) => (
                  <SelectItem key={model} value={model}>
                    {model}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>

        <div className='grid grid-cols-2 gap-3'>
          {props.sizes.length > 0 ? (
            <div className='grid gap-2'>
              <Label htmlFor='image-size'>{t('Size')}</Label>
              <Select
                value={props.value.size}
                onValueChange={(next) => {
                  if (next !== null) props.onChange({ size: next })
                }}
                disabled={props.isGenerating}
              >
                <SelectTrigger id='image-size' className='w-full'>
                  <SelectValue>
                    {IMAGE_PARAMETER_LABEL_KEYS[props.value.size]
                      ? t(IMAGE_PARAMETER_LABEL_KEYS[props.value.size])
                      : props.value.size}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {props.sizes.map((size) => (
                      <SelectItem key={size} value={size}>
                        {IMAGE_PARAMETER_LABEL_KEYS[size]
                          ? t(IMAGE_PARAMETER_LABEL_KEYS[size])
                          : size}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
          ) : null}
          {props.qualities.length > 0 ? (
            <div className='grid gap-2'>
              <Label htmlFor='image-quality'>{t('Quality')}</Label>
              <Select
                value={props.value.quality}
                onValueChange={(next) => {
                  if (next !== null) props.onChange({ quality: next })
                }}
                disabled={props.isGenerating}
              >
                <SelectTrigger id='image-quality' className='w-full'>
                  <SelectValue>
                    {IMAGE_PARAMETER_LABEL_KEYS[props.value.quality]
                      ? t(IMAGE_PARAMETER_LABEL_KEYS[props.value.quality])
                      : props.value.quality}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {props.qualities.map((quality) => (
                      <SelectItem key={quality} value={quality}>
                        {IMAGE_PARAMETER_LABEL_KEYS[quality]
                          ? t(IMAGE_PARAMETER_LABEL_KEYS[quality])
                          : quality}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
          ) : null}
          {props.styles.length > 0 ? (
            <div className='grid gap-2'>
              <Label htmlFor='image-style'>{t('Style')}</Label>
              <Select
                value={props.value.style}
                onValueChange={(next) => {
                  if (next !== null) props.onChange({ style: next })
                }}
                disabled={props.isGenerating}
              >
                <SelectTrigger id='image-style' className='w-full'>
                  <SelectValue>
                    {IMAGE_PARAMETER_LABEL_KEYS[props.value.style]
                      ? t(IMAGE_PARAMETER_LABEL_KEYS[props.value.style])
                      : props.value.style}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {props.styles.map((style) => (
                      <SelectItem key={style} value={style}>
                        {IMAGE_PARAMETER_LABEL_KEYS[style]
                          ? t(IMAGE_PARAMETER_LABEL_KEYS[style])
                          : style}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
          ) : null}
          <div className='grid gap-2'>
            <Label htmlFor='image-count'>{t('Count')}</Label>
            <Select
              value={String(props.value.count)}
              onValueChange={(next) => {
                if (next !== null) props.onChange({ count: Number(next) })
              }}
              disabled={props.isGenerating}
            >
              <SelectTrigger id='image-count' className='w-full'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {Array.from(
                    { length: countLimit - IMAGE_COUNT_MIN + 1 },
                    (_, index) => IMAGE_COUNT_MIN + index
                  ).map((count) => (
                    <SelectItem key={count} value={String(count)}>
                      {String(count)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
        </div>

        <ReferenceUploader
          items={props.references}
          maxAllowed={props.maxReferenceImages}
          disabled={props.isGenerating}
          errorKey={props.referenceErrorKey}
          onAddFiles={props.onAddReferenceFiles}
          onRemove={props.onRemoveReference}
        />

        {props.formError ? (
          <Alert variant='destructive'>
            <AlertDescription role='alert'>{props.formError}</AlertDescription>
          </Alert>
        ) : null}

        <div className='flex gap-2'>
          {props.isGenerating ? (
            <Button type='button' variant='outline' onClick={props.onCancel}>
              {t('Cancel')}
            </Button>
          ) : (
            <Button
              type='button'
              onClick={props.onGenerate}
              disabled={!canGenerate}
            >
              {t('Generate')}
            </Button>
          )}
        </div>
      </CardContent>
    </Card>
  )
}
