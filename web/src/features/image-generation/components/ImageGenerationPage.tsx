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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'

import { useImageData } from '../hooks/use-image-data'
import { useImageGeneration } from '../hooks/use-image-generation'
import { useImageHistory } from '../hooks/use-image-history'
import { useReferenceImages } from '../hooks/use-reference-images'
import {
  getModelCapability,
  resolveSettingsForModel,
} from '../lib/model-capabilities'
import { GenerationForm } from './GenerationForm'
import { HistoryPanel } from './HistoryPanel'
import { ResultGallery } from './ResultGallery'

export function ImageGenerationPage() {
  const { t } = useTranslation()
  const data = useImageData()
  const history = useImageHistory()
  const generation = useImageGeneration()

  const [prompt, setPrompt] = useState('')
  const [size, setSize] = useState('')
  const [quality, setQuality] = useState('')
  const [style, setStyle] = useState('')
  const [count, setCount] = useState(1)
  const [hasAttempted, setHasAttempted] = useState(false)

  const capability = getModelCapability(data.model)

  useEffect(() => {
    if (!data.model) return
    const resolved = resolveSettingsForModel(data.model, {
      size,
      quality,
      style,
      count,
    })
    if (resolved.size !== size) setSize(resolved.size)
    if (resolved.quality !== quality) setQuality(resolved.quality)
    if (resolved.style !== style) setStyle(resolved.style)
    if (resolved.count !== count) setCount(resolved.count)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data.model])

  const references = useReferenceImages(
    data.model,
    capability.maxReferenceImages
  )

  const handleModelChange = (nextModel: string) => {
    data.setModel(nextModel)
    const resolved = resolveSettingsForModel(nextModel, {
      size,
      quality,
      style,
      count,
    })
    setSize(resolved.size)
    setQuality(resolved.quality)
    setStyle(resolved.style)
    setCount(resolved.count)
  }

  const handleGenerate = async () => {
    setHasAttempted(true)
    const images = await generation.generate({
      apiKeyId: data.apiKeyId,
      model: data.model,
      prompt,
      count,
      size,
      quality,
      style,
      images: references.items.map((item) => item.file),
    })
    if (images && images.length > 0) {
      history.pushEntry({
        prompt: prompt.trim(),
        model: data.model,
        size,
        quality,
        style,
        count,
        images,
      })
    }
  }

  if (data.isLoadingBootstrap) {
    return (
      <div className='grid gap-4' aria-live='polite'>
        <Skeleton className='h-8 w-48' />
        <Skeleton className='h-64 w-full' />
        <span className='sr-only'>{t('Loading image generation...')}</span>
      </div>
    )
  }

  if (data.isBootstrapError) {
    return (
      <Alert variant='destructive'>
        <AlertTitle>{t('Failed to load image generation')}</AlertTitle>
        <AlertDescription className='mt-2 flex flex-wrap items-center gap-2'>
          <span>{t('Please check your connection and try again.')}</span>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void data.refetchBootstrap()}
          >
            {t('Retry')}
          </Button>
        </AlertDescription>
      </Alert>
    )
  }

  if (data.keys.length === 0) {
    return (
      <Alert>
        <AlertTitle>{t('No API keys available')}</AlertTitle>
        <AlertDescription className='mt-2 flex flex-wrap items-center gap-2'>
          <span>
            {t('Create an API key first, then return to generate images.')}
          </span>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void data.refetchBootstrap()}
          >
            {t('Refresh')}
          </Button>
        </AlertDescription>
      </Alert>
    )
  }

  let modelNotice: {
    title: string
    description: string
    destructive: boolean
    canRetry: boolean
  } | null = null
  if (data.selectedKey && data.availableModels.length === 0) {
    const groupLoading = data.isLoadingGroupModels
    let modelsTitle = t('No image models available')
    let modelsDescription = t(
      'This API key has no image models. Try another key or refresh.'
    )
    if (groupLoading) {
      modelsTitle = t('Loading models...')
      modelsDescription = t('Loading available models for this API key.')
    } else if (data.isGroupModelsError) {
      modelsTitle = t('Loading failed')
      modelsDescription = t('Please check your connection and try again.')
    }
    modelNotice = {
      title: modelsTitle,
      description: modelsDescription,
      destructive: !groupLoading && data.isGroupModelsError,
      canRetry: !groupLoading,
    }
  }

  return (
    <div className='grid gap-4'>
      <div>
        <h1 className='text-xl font-semibold tracking-tight'>
          {t('Image Generation')}
        </h1>
        <p className='text-muted-foreground text-sm'>
          {t('Turn text prompts into images. Results stay in this browser.')}
        </p>
      </div>
      <div aria-live='polite' className='sr-only'>
        {generation.isGenerating ? t('Generating images...') : null}
      </div>
      {modelNotice ? (
        <Alert variant={modelNotice.destructive ? 'destructive' : undefined}>
          <AlertTitle>{modelNotice.title}</AlertTitle>
          <AlertDescription className='mt-2 flex flex-wrap items-center gap-2'>
            <span>{modelNotice.description}</span>
            {modelNotice.canRetry ? (
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => void data.refetchGroupModels()}
              >
                {t('Retry')}
              </Button>
            ) : null}
          </AlertDescription>
        </Alert>
      ) : null}
      <div className='grid items-start gap-4 lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]'>
        <GenerationForm
          value={{
            prompt,
            apiKeyId: data.apiKeyId,
            model: data.model,
            size,
            quality,
            style,
            count,
          }}
          onChange={(patch) => {
            if (patch.prompt !== undefined) setPrompt(patch.prompt)
            if (patch.apiKeyId !== undefined) data.setApiKeyId(patch.apiKeyId)
            if (patch.model !== undefined) handleModelChange(patch.model)
            if (patch.size !== undefined) setSize(patch.size)
            if (patch.quality !== undefined) setQuality(patch.quality)
            if (patch.style !== undefined) setStyle(patch.style)
            if (patch.count !== undefined) setCount(patch.count)
          }}
          keys={data.keys}
          models={data.availableModels}
          sizes={capability.sizes}
          qualities={capability.qualities}
          styles={capability.styles}
          maxCount={capability.maxN}
          maxReferenceImages={capability.maxReferenceImages}
          references={references.items}
          referenceErrorKey={references.errorKey}
          onAddReferenceFiles={references.addFiles}
          onRemoveReference={references.removeItem}
          isGenerating={generation.isGenerating}
          formError={generation.error}
          isInitializing={data.isLoadingGroupModels}
          onGenerate={() => void handleGenerate()}
          onCancel={generation.cancel}
        />
        <div className='grid gap-4'>
          <ResultGallery
            results={generation.results}
            isGenerating={generation.isGenerating}
            hasAttempted={hasAttempted}
          />
          <HistoryPanel
            entries={history.visibleEntries}
            totalCount={history.entries.length}
            query={history.query}
            onQueryChange={history.setQuery}
            orientation={history.orientation}
            onOrientationChange={history.setOrientation}
            onRemove={history.removeEntry}
            onClear={history.clearAll}
            onKeepLocally={history.keepLocally}
            resolveUrl={history.resolveResultUrl}
          />
        </div>
      </div>
    </div>
  )
}
