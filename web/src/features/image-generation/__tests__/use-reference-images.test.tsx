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
import { act, renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { useReferenceImages } from '../hooks/use-reference-images'

let previewCounter = 0

beforeEach(() => {
  previewCounter = 0
  URL.createObjectURL = vi.fn(() => {
    previewCounter += 1
    return `blob:preview-${previewCounter}`
  }) as typeof URL.createObjectURL
  URL.revokeObjectURL = vi.fn()
})

function png(name: string) {
  return new File(['image-bytes'], name, { type: 'image/png' })
}

describe('useReferenceImages releases previews created after mount', () => {
  test('unmount revokes every preview URL added during the session', () => {
    const { result, unmount } = renderHook(() =>
      useReferenceImages('gpt-image-2', 4)
    )

    act(() => {
      expect(result.current.addFiles([png('a.png'), png('b.png')])).toBe(true)
    })
    expect(result.current.items.map((item) => item.previewUrl)).toEqual([
      'blob:preview-1',
      'blob:preview-2',
    ])

    unmount()

    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:preview-1')
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:preview-2')
    expect(vi.mocked(URL.revokeObjectURL).mock.calls).toHaveLength(2)
  })

  test('removing one image revokes only its own preview', () => {
    const { result, unmount } = renderHook(() =>
      useReferenceImages('gpt-image-2', 4)
    )

    act(() => {
      result.current.addFiles([png('a.png'), png('b.png')])
    })
    const [first] = result.current.items

    act(() => {
      result.current.removeItem(first.id)
    })

    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:preview-1')

    unmount()
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:preview-2')
  })
})
