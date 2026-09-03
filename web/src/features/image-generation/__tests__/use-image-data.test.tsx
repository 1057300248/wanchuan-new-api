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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { loadGroupModels, loadImagePageBootstrap } from '../api'
import { useImageData } from '../hooks/use-image-data'
import type { ImageApiKeyOption } from '../types'

vi.mock('../api', () => ({
  loadGroupModels: vi.fn(),
  loadImagePageBootstrap: vi.fn(),
}))

const bootstrapMock = vi.mocked(loadImagePageBootstrap)
const groupModelsMock = vi.mocked(loadGroupModels)

function makeKey(id: number, group: string): ImageApiKeyOption {
  return {
    id,
    name: `key-${id}`,
    group,
    model_limits_enabled: false,
    model_limits: '',
    status: 1,
  }
}

const PRICING = [
  { model_name: 'dall-e-3', supported_endpoint_types: ['image-generation'] },
  { model_name: 'gpt-image-2', supported_endpoint_types: ['image-generation'] },
  { model_name: 'gpt-4o', supported_endpoint_types: ['chat'] },
]

const GROUP_MODELS: Record<string, string[]> = {
  g1: ['dall-e-3', 'gpt-image-2'],
  '': ['gpt-image-2'],
  g2: [],
}

let bootstrapKeys: ImageApiKeyOption[] = []

function createWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return function Wrapper(props: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>
        {props.children}
      </QueryClientProvider>
    )
  }
}

beforeEach(() => {
  bootstrapKeys = [makeKey(1, 'g1')]
  bootstrapMock.mockImplementation(async () => ({
    keys: bootstrapKeys,
    pricingModels: PRICING,
  }))
  groupModelsMock.mockImplementation(async (group: string) => {
    return GROUP_MODELS[group] ?? []
  })
})

describe('useImageData selects keys and models as availability changes', () => {
  test('requests group models even when the selected key has an empty group', async () => {
    bootstrapKeys = [makeKey(2, '')]
    const { result } = renderHook(() => useImageData(), {
      wrapper: createWrapper(),
    })

    await waitFor(() => expect(result.current.apiKeyId).toBe(2))
    await waitFor(() => expect(groupModelsMock).toHaveBeenCalledWith(''))
    await waitFor(() =>
      expect(result.current.availableModels).toEqual(['gpt-image-2'])
    )
  })

  test('selects the first key again when the current key disappears after refresh', async () => {
    bootstrapKeys = [makeKey(1, 'g1'), makeKey(2, '')]
    const { result } = renderHook(() => useImageData(), {
      wrapper: createWrapper(),
    })
    await waitFor(() => expect(result.current.apiKeyId).toBe(1))

    bootstrapKeys = [makeKey(2, '')]
    await act(async () => {
      await result.current.refetchBootstrap()
    })

    await waitFor(() => expect(result.current.apiKeyId).toBe(2))
  })

  test('moves the model to the first available option after the key changes', async () => {
    bootstrapKeys = [makeKey(1, 'g1'), makeKey(2, '')]
    const { result } = renderHook(() => useImageData(), {
      wrapper: createWrapper(),
    })
    await waitFor(() => expect(result.current.model).toBe('dall-e-3'))

    act(() => {
      result.current.setApiKeyId(2)
    })

    await waitFor(() => expect(result.current.model).toBe('gpt-image-2'))
  })

  test('clears the model when the new key offers no image models', async () => {
    bootstrapKeys = [makeKey(1, 'g1'), makeKey(3, 'g2')]
    const { result } = renderHook(() => useImageData(), {
      wrapper: createWrapper(),
    })
    await waitFor(() => expect(result.current.model).toBe('dall-e-3'))

    act(() => {
      result.current.setApiKeyId(3)
    })

    await waitFor(() => expect(result.current.model).toBe(''))
  })
})
