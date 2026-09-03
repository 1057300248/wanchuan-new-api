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
import { IMAGE_BLOB_STORE_NAME } from '../constants'

const DB_NAME = 'image-generation-db'
const DB_VERSION = 1

function openDatabase(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    if (typeof indexedDB === 'undefined') {
      reject(new Error('IndexedDB is not available'))
      return
    }
    const request = indexedDB.open(DB_NAME, DB_VERSION)
    request.addEventListener('upgradeneeded', () => {
      const db = request.result
      if (!db.objectStoreNames.contains(IMAGE_BLOB_STORE_NAME)) {
        db.createObjectStore(IMAGE_BLOB_STORE_NAME)
      }
    })
    request.addEventListener('success', () => resolve(request.result))
    request.addEventListener('error', () =>
      reject(request.error ?? new Error('Failed to open IndexedDB'))
    )
  })
}

export async function saveImageBlob(key: string, blob: Blob): Promise<void> {
  const db = await openDatabase()
  try {
    await new Promise<void>((resolve, reject) => {
      const tx = db.transaction(IMAGE_BLOB_STORE_NAME, 'readwrite')
      tx.objectStore(IMAGE_BLOB_STORE_NAME).put(blob, key)
      tx.addEventListener('complete', () => resolve())
      tx.addEventListener('error', () =>
        reject(tx.error ?? new Error('Failed to save blob'))
      )
    })
  } finally {
    db.close()
  }
}

export async function loadImageBlob(key: string): Promise<Blob | null> {
  const db = await openDatabase()
  try {
    return await new Promise<Blob | null>((resolve, reject) => {
      const tx = db.transaction(IMAGE_BLOB_STORE_NAME, 'readonly')
      const request = tx.objectStore(IMAGE_BLOB_STORE_NAME).get(key)
      request.addEventListener('success', () => {
        const value = request.result as Blob | undefined
        resolve(value ?? null)
      })
      request.addEventListener('error', () =>
        reject(request.error ?? new Error('Failed to load blob'))
      )
    })
  } finally {
    db.close()
  }
}

export async function deleteImageBlob(key: string): Promise<void> {
  const db = await openDatabase()
  try {
    await new Promise<void>((resolve, reject) => {
      const tx = db.transaction(IMAGE_BLOB_STORE_NAME, 'readwrite')
      tx.objectStore(IMAGE_BLOB_STORE_NAME).delete(key)
      tx.addEventListener('complete', () => resolve())
      tx.addEventListener('error', () =>
        reject(tx.error ?? new Error('Failed to delete blob'))
      )
    })
  } finally {
    db.close()
  }
}
