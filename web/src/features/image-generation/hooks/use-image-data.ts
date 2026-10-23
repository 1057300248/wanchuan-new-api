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
import { skipToken, useQuery } from '@tanstack/react-query'
import { CanceledError } from 'axios'
import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from 'react'

import { getAuthSessionEpoch } from '@/lib/auth-session'
import { useAuthStore } from '@/stores/auth-store'

import { loadImageTokenModels, loadImagePageBootstrap } from '../api'
import { getAvailableImageModels } from '../lib/model-filter'
import { getImageHistoryOwnerId } from '../storage/history-owner'

export function useImageData() {
  const ownerEpoch = useSyncExternalStore(useAuthStore.subscribe, getAuthSessionEpoch)
  const sessionReady = useAuthStore((state) =>
    state.auth.bootstrapState === 'complete' &&
    Boolean(state.auth.accessToken && state.auth.session?.sid)
  )
  const ownerId = sessionReady ? getImageHistoryOwnerId() : null
  const isCurrentOwner = useCallback(() =>
    ownerId !== null &&
    ownerEpoch === getAuthSessionEpoch() &&
    useAuthStore.getState().auth.bootstrapState === 'complete' &&
    getImageHistoryOwnerId() === ownerId,
    [ownerEpoch, ownerId]
  )
  const assertCurrentOwner = useCallback((signal: AbortSignal) => {
    if (signal.aborted || !isCurrentOwner()) {
      throw new CanceledError('Image query owner changed')
    }
  }, [isCurrentOwner])
  const [apiKeyId, setApiKeyId] = useState<number | null>(null)
  const [model, setModel] = useState('')

  const bootstrap = useQuery({
    queryKey: ['image-generation-bootstrap', ownerId, ownerEpoch],
    enabled: ownerId !== null,
    queryFn: ({ signal }) =>
      loadImagePageBootstrap(() => assertCurrentOwner(signal)),
  })

  const bootstrapData = ownerId === null ? undefined : bootstrap.data
  const keys = useMemo(() => bootstrapData?.keys ?? [], [bootstrapData?.keys])
  const pricingModels = useMemo(
    () => bootstrapData?.pricingModels ?? [],
    [bootstrapData?.pricingModels]
  )

  const selectedKey = useMemo(
    () => keys.find((key) => key.id === apiKeyId) ?? null,
    [keys, apiKeyId]
  )
  const group = selectedKey?.group ?? ''

  const tokenModelsQuery = useQuery({
    queryKey: [
      'image-generation-token-models',
      ownerId,
      ownerEpoch,
      selectedKey?.id ?? null,
      group,
      selectedKey?.auto_groups ?? null,
      selectedKey?.model_limits_enabled ?? false,
      selectedKey?.model_limits ?? '',
    ],
    enabled: ownerId !== null && selectedKey !== null,
    queryFn: selectedKey
      ? async ({ signal }) => {
          assertCurrentOwner(signal)
          const models = await loadImageTokenModels(selectedKey.id)
          assertCurrentOwner(signal)
          return models
        }
      : skipToken,
    staleTime: 0,
  })

  const tokenModels = useMemo(
    () => (ownerId === null || tokenModelsQuery.isError ? [] : (tokenModelsQuery.data ?? [])),
    [ownerId, tokenModelsQuery.data, tokenModelsQuery.isError]
  )

  const availableModels = useMemo(
    () =>
      getAvailableImageModels({
        pricingModels,
        tokenModels,
        token: selectedKey,
      }),
    [pricingModels, tokenModels, selectedKey]
  )

  // QueryObserver.refetch ignores enabled. Guard even callbacks retained by a
  // mounted page from before logout, without suppressing genuine auth failures.
  const { refetch: refetchBootstrapQuery } = bootstrap
  const { refetch: refetchTokenModelsQuery } = tokenModelsQuery
  const refetchBootstrap = useCallback(
    () =>
      isCurrentOwner() ? refetchBootstrapQuery() : Promise.resolve(undefined),
    [isCurrentOwner, refetchBootstrapQuery]
  )
  const refetchTokenModels = useCallback(
    () =>
      isCurrentOwner() && selectedKey
        ? refetchTokenModelsQuery()
        : Promise.resolve(undefined),
    [isCurrentOwner, selectedKey, refetchTokenModelsQuery]
  )

  const effectiveApiKeyId = selectedKey?.id ?? null

  useEffect(() => {
    setApiKeyId(null)
    setModel('')
  }, [ownerEpoch, ownerId])

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
    tokenModels,
    availableModels,
    selectedKey,
    apiKeyId: effectiveApiKeyId,
    setApiKeyId,
    model,
    setModel,
    group,
    isLoadingBootstrap: ownerId !== null && bootstrap.isLoading,
    isBootstrapError: ownerId !== null && bootstrap.isError,
    bootstrapError: ownerId === null ? null : bootstrap.error,
    refetchBootstrap,
    isLoadingTokenModels: ownerId !== null && tokenModelsQuery.isLoading,
    isTokenModelsError: ownerId !== null && tokenModelsQuery.isError,
    refetchTokenModels,
  }
}
