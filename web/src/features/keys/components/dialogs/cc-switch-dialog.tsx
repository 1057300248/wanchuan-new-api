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
import { useQuery } from '@tanstack/react-query'
import { useState, useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getCCSwitchTokenModels } from '../../api'
import type { CCSwitchModelsResponse } from '../../api'
import type { ApiKey, CCSwitchApp } from '../../types'
import { ApiKeyGroupCombobox } from '../api-key-group-combobox'
import {
  buildValidatedCCSwitchURL,
  ccSwitchModelsQueryKey,
} from './cc-switch-utils'

const APP_CONFIGS = {
  claude: {
    label: 'Claude',
    defaultName: 'My Claude',
    modelFields: [
      { key: 'model', labelKey: 'Primary Model', required: true },
      { key: 'haikuModel', labelKey: 'Haiku Model', required: false },
      { key: 'sonnetModel', labelKey: 'Sonnet Model', required: false },
      { key: 'opusModel', labelKey: 'Opus Model', required: false },
    ],
  },
  codex: {
    label: 'Codex',
    defaultName: 'My Codex',
    modelFields: [{ key: 'model', labelKey: 'Primary Model', required: true }],
  },
  gemini: {
    label: 'Gemini',
    defaultName: 'My Gemini',
    modelFields: [{ key: 'model', labelKey: 'Primary Model', required: true }],
  },
} as const

type AppType = CCSwitchApp

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  tokenKey: string
  token: ApiKey | null
}

export function CCSwitchDialog(props: Props) {
  // A new opening or token scope gets fresh selections. Unmounting also
  // invalidates a pending import so an old key cannot open after switching keys.
  const scopeKey = JSON.stringify([
    props.open,
    ...ccSwitchModelsQueryKey(props.token, 'claude'),
  ])
  return <CCSwitchDialogContent key={scopeKey} {...props} />
}

function CCSwitchDialogContent(props: Props) {
  const { t } = useTranslation()
  const [app, setApp] = useState<AppType>('claude')
  const [group, setGroup] = useState('')
  const [name, setName] = useState<string>(APP_CONFIGS.claude.defaultName)
  const [models, setModels] = useState<Record<string, string>>({})
  const [isSubmitting, setIsSubmitting] = useState(false)
  const submitting = useRef(false)
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  const {
    data: modelsData,
    isFetching,
    isError,
    refetch,
  } = useQuery({
    queryKey: ccSwitchModelsQueryKey(props.token, app, group),
    queryFn: async (): Promise<CCSwitchModelsResponse> => {
      if (!props.token) return { success: true, data: [], groups: [] }
      return requireServerSuccess(
        await getCCSwitchTokenModels(props.token.id, app, group)
      )
    },
    // Keep group choices visible during a group switch, never old models.
    placeholderData: (previous) =>
      previous ? { ...previous, data: [] } : undefined,
    enabled: props.open && !!props.token,
    staleTime: 5 * 60 * 1000,
  })

  const modelOptions = useMemo(() => {
    const items = modelsData?.data ?? []
    return items.map((modelName) => ({ value: modelName, label: modelName }))
  }, [modelsData?.data])

  const groupOptions = [
    {
      value: '',
      label: `${t('API Key')}: ${props.token?.group || t('Default')}`,
    },
    ...(modelsData?.groups ?? []).map((value) => ({ value, label: value })),
  ]
  const currentConfig = APP_CONFIGS[app]
  const noModelsMessage = `${t('API Key')} ${t('Group')}: ${t('No models found')}`
  const hasNoModels = !isFetching && !isError && modelOptions.length === 0

  const handleAppChange = (val: string) => {
    if (submitting.current) return
    const appVal = val as AppType
    setApp(appVal)
    setName(APP_CONFIGS[appVal].defaultName)
    setModels({})
  }

  const handleGroupChange = (value: string) => {
    if (submitting.current) return
    setGroup(value)
    setModels({})
  }

  const handleSubmit = async () => {
    if (submitting.current) return
    if (!models.model) {
      toast.warning(t('Please select a primary model'))
      return
    }
    if (!props.token || !props.tokenKey) return

    submitting.current = true
    setIsSubmitting(true)
    try {
      const latest = await refetch()
      if (!mounted.current) return
      const availableModels = latest.data?.data ?? []
      if (latest.isError || availableModels.length === 0) {
        toast.warning(noModelsMessage)
        return
      }
      const url = buildValidatedCCSwitchURL(
        app,
        name,
        models,
        props.tokenKey,
        availableModels,
        group,
        latest.data?.groups ?? []
      )
      if (!url) {
        toast.warning(t('The selected model is no longer available'))
        setModels({})
        return
      }
      window.open(url, '_blank')
      props.onOpenChange(false)
    } finally {
      submitting.current = false
      if (mounted.current) setIsSubmitting(false)
    }
  }

  const openDisabled =
    !props.token ||
    !props.tokenKey ||
    isFetching ||
    isError ||
    isSubmitting ||
    modelOptions.length === 0

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Import to CC Switch')}
      contentClassName='sm:max-w-md'
      contentHeight='auto'
      bodyClassName={
        currentConfig.modelFields.length === 1 ? 'space-y-4 pb-52' : 'space-y-4'
      }
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button onClick={handleSubmit} disabled={openDisabled}>
            {t('Open CC Switch')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='space-y-2'>
          <Label>{t('Application')}</Label>
          <RadioGroup
            value={app}
            onValueChange={handleAppChange}
            disabled={isSubmitting}
            className='flex gap-4'
          >
            {(
              Object.entries(APP_CONFIGS) as [
                AppType,
                (typeof APP_CONFIGS)[AppType],
              ][]
            ).map(([key, cfg]) => (
              <div key={key} className='flex items-center gap-2'>
                <RadioGroupItem value={key} id={`app-${key}`} />
                <Label htmlFor={`app-${key}`} className='cursor-pointer'>
                  {cfg.label}
                </Label>
              </div>
            ))}
          </RadioGroup>
        </div>

        <div className='space-y-2'>
          <Label htmlFor='cc-switch-group'>{t('Group')}</Label>
          <ApiKeyGroupCombobox
            id='cc-switch-group'
            options={groupOptions}
            value={group}
            onValueChange={handleGroupChange}
            disabled={isFetching || isSubmitting || !props.token}
          />
        </div>

        <div className='space-y-2'>
          <Label htmlFor='cc-switch-name'>{t('Name')}</Label>
          <ComboboxInput
            id='cc-switch-name'
            options={[]}
            value={name}
            onValueChange={setName}
            placeholder={currentConfig.defaultName}
            emptyText=''
            allowCustomValue
            disabled={isSubmitting}
          />
        </div>

        {hasNoModels && (
          <p className='text-muted-foreground text-sm' role='status'>
            {noModelsMessage}
          </p>
        )}

        {currentConfig.modelFields.map((field) => (
          <div key={field.key} className='space-y-2'>
            <Label
              htmlFor={`cc-switch-${field.key}`}
              required={field.required}
            >
              {t(field.labelKey)}
            </Label>
            <ComboboxInput
              id={`cc-switch-${field.key}`}
              options={modelOptions}
              value={models[field.key] || ''}
              onValueChange={(value) =>
                setModels((prev) => ({ ...prev, [field.key]: value }))
              }
              placeholder={t('Select or enter model name')}
              emptyText={t('No models found')}
              disabled={isFetching || isSubmitting}
            />
          </div>
        ))}
      </div>
    </Dialog>
  )
}
