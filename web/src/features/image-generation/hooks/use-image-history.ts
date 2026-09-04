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
import { deleteImageBlob, loadImageBlob, saveImageBlob } from '../lib/blob-store'
import {
  dataUrlToBlob,
  filterHistoryEntries,
  pruneHistoryEntries,
} from '../lib/history-utils'
import {
  createManagedObjectUrl,
  isDataSchemeUrl,
  revokeManagedObjectUrl,
} from '../lib/managed-object-urls'
import {
  clearHistoryStorage,
  loadHistoryEntries,
  saveHistoryEntries,
} from '../storage/history-storage'
import type {
  GeneratedImage,
  HistoryResultRecord,
  ImageHistoryEntry,
} from '../types'

function toHistoryEntry(params: {
  prompt: string
  model: string
  size: string
  quality: string
  style: string
  count: number
  images: GeneratedImage[]
}): {
  entry: ImageHistoryEntry
  tempObjectUrls: Record<string, string>
} {
  const now = Date.now()
  const tempObjectUrls: Record<string, string> = {}
  const results: HistoryResultRecord[] = params.images.map((image, index) => {
    const id = `${now}-${index}-${Math.random().toString(36).slice(2, 6)}`
    if (image.source === 'b64' && isDataSchemeUrl(image.url)) {
      const blob = dataUrlToBlob(image.url)
      if (blob) {
        try {
          const objectUrl = createManagedObjectUrl(blob)
          tempObjectUrls[id] = objectUrl
          return {
            id,
            type: 'b64' as const,
            source: objectUrl,
            width: image.width,
            height: image.height,
            keptLocally: false,
            createdAt: now,
          }
        } catch {
          /* fall through to storing the data URL */
        }
      }
    }
    return {
      id,
      type: image.source,
      source: image.url,
      width: image.width,
      height: image.height,
      keptLocally: false,
      createdAt: now,
    }
  })
  return {
    entry: {
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
      results,
    },
    tempObjectUrls,
  }
}

export function useImageHistory() {
  const [entries, setEntriesState] = useState<ImageHistoryEntry[]>(() =>
    loadHistoryEntries()
  )
  const [query, setQuery] = useState('')
  const [orientation, setOrientation] = useState<
    'all' | 'square' | 'landscape' | 'portrait'
  >('all')
  const [blobUrls, setBlobUrlsState] = useState<Record<string, string>>({})

  const entriesRef = useRef(entries)
  const blobUrlsRef = useRef(blobUrls)
  const mountedRef = useRef(true)

  const commitEntries = useCallback((next: ImageHistoryEntry[]) => {
    entriesRef.current = next
    setEntriesState(next)
  }, [])

  const commitBlobUrls = useCallback((next: Record<string, string>) => {
    blobUrlsRef.current = next
    setBlobUrlsState(next)
  }, [])

  useEffect(() => {
    saveHistoryEntries(entries)
  }, [entries])

  const pruneExpiredFromState = useCallback(() => {
    const previous = entriesRef.current
    if (previous.length === 0) return
    const pruned = pruneHistoryEntries(previous, Date.now())
    const keptResultIds = new Set<string>()
    for (const entry of pruned) {
      for (const result of entry.results) keptResultIds.add(result.id)
    }
    const removedResults: HistoryResultRecord[] = []
    for (const entry of previous) {
      for (const result of entry.results) {
        if (!keptResultIds.has(result.id)) removedResults.push(result)
      }
    }
    if (removedResults.length === 0 && pruned.length === previous.length) {
      return
    }
    for (const result of removedResults) {
      if (result.blobKey) {
        void deleteImageBlob(result.blobKey).catch(() => undefined)
      }
      const objectUrl = blobUrlsRef.current[result.id]
      if (objectUrl) revokeManagedObjectUrl(objectUrl)
    }
    commitEntries(pruned)
    if (removedResults.length === 0) return
    const removedIds = new Set(removedResults.map((result) => result.id))
    const nextUrls = { ...blobUrlsRef.current }
    let changed = false
    for (const id of removedIds) {
      if (id in nextUrls) {
        delete nextUrls[id]
        changed = true
      }
    }
    if (changed) commitBlobUrls(nextUrls)
  }, [commitBlobUrls, commitEntries])

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
      pruneExpiredFromState()
      const { entry, tempObjectUrls } = toHistoryEntry(params)
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
          if (objectUrl) revokeManagedObjectUrl(objectUrl)
        }
      }
      const nextUrls = { ...blobUrlsRef.current, ...tempObjectUrls }
      const evictedIds = new Set(
        evicted.flatMap((item) => item.results.map((result) => result.id))
      )
      for (const id of evictedIds) delete nextUrls[id]
      commitEntries(next)
      commitBlobUrls(nextUrls)
    },
    [commitBlobUrls, commitEntries, pruneExpiredFromState]
  )

  const removeEntry = useCallback(
    (id: string) => {
      const target = entriesRef.current.find((entry) => entry.id === id)
      if (!target) return
      for (const result of target.results) {
        if (result.blobKey) {
          void deleteImageBlob(result.blobKey).catch(() => undefined)
        }
        const objectUrl = blobUrlsRef.current[result.id]
        if (objectUrl) revokeManagedObjectUrl(objectUrl)
      }
      commitEntries(entriesRef.current.filter((entry) => entry.id !== id))
      const nextUrls = { ...blobUrlsRef.current }
      let changed = false
      for (const result of target.results) {
        if (result.id in nextUrls) {
          delete nextUrls[result.id]
          changed = true
        }
      }
      if (changed) commitBlobUrls(nextUrls)
    },
    [commitBlobUrls, commitEntries]
  )

  const clearAll = useCallback(() => {
    for (const entry of entriesRef.current) {
      for (const result of entry.results) {
        if (result.blobKey) {
          void deleteImageBlob(result.blobKey).catch(() => undefined)
        }
      }
    }
    for (const objectUrl of Object.values(blobUrlsRef.current)) {
      revokeManagedObjectUrl(objectUrl)
    }
    commitEntries([])
    commitBlobUrls({})
    clearHistoryStorage()
  }, [commitBlobUrls, commitEntries])

  const keepLocally = useCallback(
    async (entryId: string, resultId: string) => {
      const findTarget = () => {
        const entry = entriesRef.current.find((item) => item.id === entryId)
        const result = entry?.results.find((item) => item.id === resultId)
        return { entry, result }
      }
      const isStillPresent = (source: string) => {
        if (!mountedRef.current) return false
        const current = findTarget()
        if (!current.entry || !current.result) return false
        if (current.result.keptLocally) return false
        return current.result.source === source
      }
      const initial = findTarget()
      if (!initial.entry || !initial.result || initial.result.keptLocally) {
        return
      }
      const sourceAtStart = initial.result.source
      const blobKey = `${entryId}:${resultId}`
      let savedBlob = false
      let objectUrl: string | null = null
      try {
        const response = await fetch(sourceAtStart)
        if (!isStillPresent(sourceAtStart)) return
        if (!response.ok) throw new Error('keep-failed')
        const blob = await response.blob()
        if (!isStillPresent(sourceAtStart)) return
        await saveImageBlob(blobKey, blob)
        savedBlob = true
        if (!isStillPresent(sourceAtStart)) {
          await deleteImageBlob(blobKey).catch(() => undefined)
          return
        }
        try {
          objectUrl = createManagedObjectUrl(blob)
        } catch {
          await deleteImageBlob(blobKey).catch(() => undefined)
          savedBlob = false
          throw new Error('keep-failed')
        }
        if (!isStillPresent(sourceAtStart)) {
          await deleteImageBlob(blobKey).catch(() => undefined)
          savedBlob = false
          revokeManagedObjectUrl(objectUrl)
          objectUrl = null
          return
        }
        const committedUrl = objectUrl
        const nextEntries = entriesRef.current.map((item) => {
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
        commitBlobUrls({ ...blobUrlsRef.current, [resultId]: committedUrl })
        commitEntries(nextEntries)
        savedBlob = false
        objectUrl = null
      } catch {
        if (savedBlob) {
          await deleteImageBlob(blobKey).catch(() => undefined)
        }
        if (objectUrl) {
          revokeManagedObjectUrl(objectUrl)
          if (blobUrlsRef.current[resultId] === objectUrl) {
            const nextUrls = { ...blobUrlsRef.current }
            delete nextUrls[resultId]
            commitBlobUrls(nextUrls)
          }
        }
        throw new Error('keep-failed')
      }
    },
    [commitBlobUrls, commitEntries]
  )

  const resolveResultUrl = useCallback(
    (resultId: string, fallback: string) => blobUrls[resultId] ?? fallback,
    [blobUrls]
  )

  const restoreBlobUrls = useCallback(async () => {
    const snapshot = entriesRef.current
    for (const entry of snapshot) {
      for (const result of entry.results) {
        if (result.type !== 'blob' || !result.blobKey) continue
        if (blobUrlsRef.current[result.id]) continue
        let blob: Blob | null = null
        try {
          blob = await loadImageBlob(result.blobKey)
        } catch {
          continue
        }
        if (!blob) continue
        const liveResult = entriesRef.current
          .find((item) => item.id === entry.id)
          ?.results.find((item) => item.id === result.id)
        if (
          !liveResult ||
          liveResult.type !== 'blob' ||
          liveResult.blobKey !== result.blobKey
        ) {
          continue
        }
        if (blobUrlsRef.current[result.id]) continue
        if (!mountedRef.current) return
        let objectUrl: string
        try {
          objectUrl = createManagedObjectUrl(blob)
        } catch {
          continue
        }
        const confirmed = entriesRef.current
          .find((item) => item.id === entry.id)
          ?.results.find((item) => item.id === result.id)
        if (
          !mountedRef.current ||
          !confirmed ||
          confirmed.type !== 'blob' ||
          confirmed.blobKey !== result.blobKey
        ) {
          revokeManagedObjectUrl(objectUrl)
          continue
        }
        if (blobUrlsRef.current[result.id]) {
          revokeManagedObjectUrl(objectUrl)
          continue
        }
        commitBlobUrls({ ...blobUrlsRef.current, [result.id]: objectUrl })
      }
    }
  }, [commitBlobUrls])

  useEffect(() => {
    void restoreBlobUrls()
  }, [restoreBlobUrls])

  useEffect(() => {
    const timer = window.setInterval(() => {
      pruneExpiredFromState()
    }, 60_000)
    return () => window.clearInterval(timer)
  }, [pruneExpiredFromState])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      for (const url of Object.values(blobUrlsRef.current)) {
        try {
          revokeManagedObjectUrl(url)
        } catch {
          /* ignore */
        }
      }
    }
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
