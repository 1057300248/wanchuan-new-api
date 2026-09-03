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
import { useCallback, useEffect, useRef, useState } from 'react'

import { IMAGE_HISTORY_MAX_ENTRIES } from '../constants'
import {
  deleteImageBlob,
  loadImageBlob,
  saveImageBlob,
} from '../lib/blob-store'
import { filterHistoryEntries } from '../lib/history-utils'
import {
  clearHistoryStorage,
  loadHistoryEntries,
  saveHistoryEntries,
} from '../storage/history-storage'
import type { GeneratedImage, ImageHistoryEntry } from '../types'

function toHistoryEntry(params: {
  prompt: string
  model: string
  size: string
  quality: string
  style: string
  count: number
  images: GeneratedImage[]
}): ImageHistoryEntry {
  const now = Date.now()
  return {
    id: `${now}-${Math.random().toString(36).slice(2, 8)}`,
    prompt: params.prompt,
    model: params.model,
    settings: {
      size: params.size,
      quality: params.quality,
      style: params.style,
      count: params.count,
    },
    createdAt: now,
    results: params.images.map((image, index) => ({
      id: `${now}-${index}-${Math.random().toString(36).slice(2, 6)}`,
      type: image.source,
      source: image.url,
      width: image.width,
      height: image.height,
      keptLocally: false,
      createdAt: now,
    })),
  }
}

export function useImageHistory() {
  const [entries, setEntries] = useState<ImageHistoryEntry[]>(() =>
    loadHistoryEntries()
  )
  const [query, setQuery] = useState('')
  const [orientation, setOrientation] = useState<
    'all' | 'square' | 'landscape' | 'portrait'
  >('all')
  const [blobUrls, setBlobUrls] = useState<Record<string, string>>({})

  const entriesRef = useRef(entries)
  entriesRef.current = entries
  const blobUrlsRef = useRef(blobUrls)
  blobUrlsRef.current = blobUrls

  useEffect(() => {
    saveHistoryEntries(entries)
  }, [entries])

  useEffect(() => {
    return () => {
      for (const url of Object.values(blobUrlsRef.current)) {
        URL.revokeObjectURL(url)
      }
    }
  }, [])

  const pushEntry = useCallback(
    (params: {
      prompt: string
      model: string
      size: string
      quality: string
      style: string
      count: number
      images: GeneratedImage[]
    }) => {
      if (params.images.length === 0) return
      const entry = toHistoryEntry(params)
      const previous = entriesRef.current
      const next = [entry, ...previous].slice(0, IMAGE_HISTORY_MAX_ENTRIES)
      const keptIds = new Set(next.map((item) => item.id))
      const evicted = previous.filter((item) => !keptIds.has(item.id))
      for (const item of evicted) {
        for (const result of item.results) {
          if (result.blobKey) {
            void deleteImageBlob(result.blobKey).catch(() => undefined)
          }
          const objectUrl = blobUrlsRef.current[result.id]
          if (objectUrl) URL.revokeObjectURL(objectUrl)
        }
      }
      setEntries((prev) => [entry, ...prev].slice(0, IMAGE_HISTORY_MAX_ENTRIES))
      if (evicted.length > 0) {
        const evictedResultIds = new Set(
          evicted.flatMap((item) => item.results.map((result) => result.id))
        )
        setBlobUrls((prev) => {
          if (![...evictedResultIds].some((id) => id in prev)) return prev
          const urls = { ...prev }
          for (const id of evictedResultIds) delete urls[id]
          return urls
        })
      }
    },
    []
  )

  const removeEntry = useCallback((id: string) => {
    const target = entriesRef.current.find((entry) => entry.id === id)
    const resultIds = (target?.results ?? []).map((result) => result.id)
    if (target) {
      for (const result of target.results) {
        if (result.blobKey) {
          void deleteImageBlob(result.blobKey).catch(() => undefined)
        }
      }
    }
    for (const resultId of resultIds) {
      const objectUrl = blobUrlsRef.current[resultId]
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
    setEntries((prev) => prev.filter((entry) => entry.id !== id))
    if (resultIds.length > 0) {
      setBlobUrls((prev) => {
        if (!resultIds.some((resultId) => resultId in prev)) return prev
        const urls = { ...prev }
        for (const resultId of resultIds) delete urls[resultId]
        return urls
      })
    }
  }, [])

  const clearAll = useCallback(() => {
    for (const entry of entriesRef.current) {
      for (const result of entry.results) {
        if (result.blobKey) {
          void deleteImageBlob(result.blobKey).catch(() => undefined)
        }
      }
    }
    for (const objectUrl of Object.values(blobUrlsRef.current)) {
      URL.revokeObjectURL(objectUrl)
    }
    setEntries([])
    setBlobUrls({})
    clearHistoryStorage()
  }, [])

  const keepLocally = useCallback(
    async (entryId: string, resultId: string) => {
      const entry = entries.find((item) => item.id === entryId)
      const result = entry?.results.find((item) => item.id === resultId)
      if (!entry || !result || result.keptLocally) return
      try {
        const response = await fetch(result.source)
        if (!response.ok) throw new Error('fetch failed')
        const blob = await response.blob()
        const blobKey = `${entryId}:${resultId}`
        await saveImageBlob(blobKey, blob)
        const objectUrl = URL.createObjectURL(blob)
        setBlobUrls((prev) => ({ ...prev, [resultId]: objectUrl }))
        setEntries((prev) =>
          prev.map((item) => {
            if (item.id !== entryId) return item
            return {
              ...item,
              results: item.results.map((itemResult) => {
                if (itemResult.id !== resultId) return itemResult
                return {
                  ...itemResult,
                  keptLocally: true,
                  blobKey,
                  type: 'blob' as const,
                  source: blobKey,
                }
              }),
            }
          })
        )
      } catch {
        /* caller surfaces toast; keep metadata unchanged */
        throw new Error('keep-failed')
      }
    },
    [entries]
  )

  const resolveResultUrl = useCallback(
    (resultId: string, fallback: string) => blobUrls[resultId] ?? fallback,
    [blobUrls]
  )

  const restoreBlobUrls = useCallback(async () => {
    const updates: Record<string, string> = {}
    for (const entry of entries) {
      for (const result of entry.results) {
        if (result.type === 'blob' && result.blobKey && !blobUrls[result.id]) {
          try {
            const blob = await loadImageBlob(result.blobKey)
            if (blob) updates[result.id] = URL.createObjectURL(blob)
          } catch {
            /* skip unreadable blob */
          }
        }
      }
    }
    if (Object.keys(updates).length > 0) {
      setBlobUrls((prev) => ({ ...prev, ...updates }))
    }
  }, [entries, blobUrls])

  useEffect(() => {
    void restoreBlobUrls()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const visibleEntries = filterHistoryEntries(entries, query, orientation)

  return {
    entries,
    visibleEntries,
    query,
    setQuery,
    orientation,
    setOrientation,
    pushEntry,
    removeEntry,
    clearAll,
    keepLocally,
    resolveResultUrl,
  }
}
