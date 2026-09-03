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
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { requestImageGeneration } from '../api'

vi.mock('@/features/keys/api', () => ({
  fetchTokenKey: vi.fn(),
  getApiKeys: vi.fn(),
}))

vi.mock('@/features/pricing/api', () => ({
  getPricing: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
}))

const postMock = vi.mocked(api.post)

beforeEach(() => {
  postMock.mockReset()
})

describe('requestImageGeneration selects the synchronous image endpoint', () => {
  test('sends JSON to generations when no reference image is present', async () => {
    postMock.mockResolvedValue({
      data: { data: [{ url: 'https://cdn.example/generated.png' }] },
    })
    const signal = new AbortController().signal

    const images = await requestImageGeneration({
      fullKey: 'sk-test',
      model: 'dall-e-3',
      prompt: 'a lake',
      count: 1,
      size: '1024x1024',
      quality: 'standard',
      style: 'vivid',
      images: [],
      signal,
    })

    expect(postMock).toHaveBeenCalledWith(
      '/v1/images/generations',
      {
        model: 'dall-e-3',
        prompt: 'a lake',
        n: 1,
        response_format: 'url',
        size: '1024x1024',
        quality: 'standard',
        style: 'vivid',
      },
      {
        headers: { Authorization: 'Bearer sk-test' },
        signal,
        skipErrorHandler: true,
      }
    )
    expect(images[0]).toMatchObject({
      url: 'https://cdn.example/generated.png',
      source: 'url',
    })
  })

  test('sends multipart data to edits when reference images are present', async () => {
    postMock.mockResolvedValue({
      data: { data: [{ b64_json: 'aGVsbG8=' }] },
    })
    const file = new File(['image'], 'reference.png', { type: 'image/png' })

    const images = await requestImageGeneration({
      fullKey: 'sk-test',
      model: 'gpt-image-2',
      prompt: 'edit this',
      count: 2,
      size: '1024x1024',
      quality: 'medium',
      style: '',
      images: [file],
    })

    expect(postMock).toHaveBeenCalledTimes(1)
    const [url, body, config] = postMock.mock.calls[0]
    expect(url).toBe('/v1/images/edits')
    expect(body).toBeInstanceOf(FormData)
    const form = body as FormData
    expect(form.get('model')).toBe('gpt-image-2')
    expect(form.get('prompt')).toBe('edit this')
    const uploaded = form.get('image') as File
    expect(uploaded.name).toBe(file.name)
    expect(uploaded.type).toBe(file.type)
    expect(uploaded.size).toBe(file.size)
    expect(form.get('n')).toBe('2')
    expect(config).toMatchObject({
      headers: { Authorization: 'Bearer sk-test' },
      skipErrorHandler: true,
    })
    expect(images[0]).toMatchObject({
      source: 'b64',
      width: 1024,
      height: 1024,
    })
  })
})
