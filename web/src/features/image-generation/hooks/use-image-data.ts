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
import { useEffect, useMemo, useState } from 'react'

import { loadGroupModels, loadImagePageBootstrap } from '../api'
import { getAvailableImageModels } from '../lib/model-filter'

export function useImageData() {
  const [apiKeyId, setApiKeyId] = useState<number | null>(null)
  const [model, setModel] = useState('')

  const bootstrap = useQuery({
    queryKey: ['image-generation-bootstrap'],
    queryFn: loadImagePageBootstrap,
  })

  const keys = useMemo(() => bootstrap.data?.keys ?? [], [bootstrap.data?.keys])
  const pricingModels = useMemo(
    () => bootstrap.data?.pricingModels ?? [],
    [bootstrap.data?.pricingModels]
  )

  const selectedKey = useMemo(
    () => keys.find((key) => key.id === apiKeyId) ?? null,
    [keys, apiKeyId]
  )
  const group = selectedKey?.group ?? ''

  const groupModelsQuery = useQuery({
    queryKey: ['image-generation-group-models', group],
    queryFn: () => loadGroupModels(group),
    enabled: selectedKey !== null,
  })

  const groupModels = useMemo(
    () => groupModelsQuery.data ?? [],
    [groupModelsQuery.data]
  )

  const availableModels = useMemo(
    () =>
      getAvailableImageModels({
        pricingModels,
        groupModels,
        token: selectedKey,
      }),
    [pricingModels, groupModels, selectedKey]
  )

  const effectiveApiKeyId = apiKeyId ?? selectedKey?.id ?? null

  useEffect(() => {
    if (keys.length === 0) return
    if (apiKeyId === null || !keys.some((key) => key.id === apiKeyId)) {
      setApiKeyId(keys[0].id)
    }
  }, [keys, apiKeyId])

  useEffect(() => {
    if (!availableModels.includes(model)) {
      setModel(availableModels.length > 0 ? availableModels[0] : '')
    }
  }, [availableModels, model])

  return {
    keys,
    pricingModels,
    groupModels,
    availableModels,
    selectedKey,
    apiKeyId: effectiveApiKeyId,
    setApiKeyId,
    model,
    setModel,
    group,
    isLoadingBootstrap: bootstrap.isLoading,
    isBootstrapError: bootstrap.isError,
    bootstrapError: bootstrap.error,
    refetchBootstrap: bootstrap.refetch,
    isLoadingGroupModels: groupModelsQuery.isLoading,
    isGroupModelsError: groupModelsQuery.isError,
    refetchGroupModels: groupModelsQuery.refetch,
  }
}
