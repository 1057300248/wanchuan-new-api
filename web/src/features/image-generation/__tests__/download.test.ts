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
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { downloadImage } from '../api'

const createdAnchors: HTMLAnchorElement[] = []
const realCreateElement = document.createElement.bind(document)

beforeEach(() => {
  createdAnchors.length = 0
  vi.spyOn(document, 'createElement').mockImplementation(((
    tagName: string,
    ...rest: never[]
  ) => {
    const element = realCreateElement(
      tagName as 'div',
      ...(rest as [])
    ) as HTMLElement
    if (tagName === 'a') createdAnchors.push(element as HTMLAnchorElement)
    return element
  }) as typeof document.createElement)
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(
    () => undefined
  )
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('downloadImage degrades gracefully across origins', () => {
  test('saves data URLs through a temporary anchor without any fetch', async () => {
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)

    await downloadImage('data:image/png;base64,aGVsbG8=', 'a.png')

    expect(fetchSpy).not.toHaveBeenCalled()
    expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledTimes(1)
    expect(createdAnchors).toHaveLength(1)
    expect(createdAnchors[0].href).toBe('data:image/png;base64,aGVsbG8=')
    expect(createdAnchors[0].download).toBe('a.png')
    expect(document.body.querySelector('a')).toBeNull()
  })

  test('saves fetchable URLs as object URLs and revokes them afterwards', async () => {
    const payload = new Blob(['image-bytes'], { type: 'image/png' })
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, blob: async () => payload }))
    )
    const created: string[] = []
    const revoked: string[] = []
    URL.createObjectURL = vi.fn(() => {
      const url = `blob:mock-${created.length}`
      created.push(url)
      return url
    }) as typeof URL.createObjectURL
    URL.revokeObjectURL = vi.fn((url: string) => {
      revoked.push(url)
    })

    await downloadImage('https://cdn.example/a.png', 'a.png')

    expect(created).toEqual(['blob:mock-0'])
    expect(createdAnchors).toHaveLength(1)
    expect(createdAnchors[0].download).toBe('a.png')
    expect(revoked).toEqual(['blob:mock-0'])
    expect(document.body.querySelector('a')).toBeNull()
  })

  test('opens the raw URL in a new tab instead of failing when fetch is blocked', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch')
      })
    )

    await expect(
      downloadImage('https://cdn.example/blocked.png', 'blocked.png')
    ).resolves.toBeUndefined()

    expect(createdAnchors).toHaveLength(1)
    expect(createdAnchors[0].href).toBe('https://cdn.example/blocked.png')
    expect(createdAnchors[0].download).toBe('blocked.png')
    expect(createdAnchors[0].target).toBe('_blank')
    expect(createdAnchors[0].rel).toContain('noopener')
    expect(document.body.querySelector('a')).toBeNull()
  })
})
