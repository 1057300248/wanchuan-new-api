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
  test('Console generations send aspect ratio, explicit 1K resolution and selected quality', async () => {
    postMock.mockResolvedValue({ data: { data: [] } })
    await requestImageGeneration({
      fullKey: 'fixture',
      model: 'grok-imagine-image-2.0-console',
      prompt: 'A lake',
      count: 1,
      size: '16:9',
      quality: 'medium',
      style: '',
      images: [],
    })
    expect(postMock.mock.calls[0][0]).toBe('/v1/images/generations')
    expect(postMock.mock.calls[0][1]).toEqual({
      model: 'grok-imagine-image-2.0-console',
      prompt: 'A lake',
      n: 1,
      aspect_ratio: '16:9',
      resolution: '1k',
      quality: 'medium',
      response_format: 'b64_json',
    })
  })

  test('Console reference images are sent as JSON data URLs, not multipart, with quality preserved', async () => {
    postMock.mockResolvedValue({ data: { data: [] } })
    await requestImageGeneration({
      fullKey: 'fixture',
      model: 'grok-imagine-image-2.0-console',
      prompt: 'Edit this',
      count: 2,
      size: '3:2',
      quality: 'low',
      style: '',
      images: [
        new File(['hello'], 'a.png', { type: 'image/png' }),
        new File(['world'], 'b.jpg', { type: 'image/jpeg' }),
      ],
    })
    expect(postMock.mock.calls[0][0]).toBe('/v1/images/edits')
    expect(postMock.mock.calls[0][1]).toEqual({
      model: 'grok-imagine-image-2.0-console',
      prompt: 'Edit this',
      n: 2,
      aspect_ratio: '3:2',
      resolution: '1k',
      quality: 'low',
      response_format: 'b64_json',
      images: [
        { url: 'data:image/png;base64,aGVsbG8=' },
        { url: 'data:image/jpeg;base64,d29ybGQ=' },
      ],
    })
  })

  test('Console rejects more than three references instead of silently discarding files', async () => {
    postMock.mockResolvedValue({ data: { data: [] } })
    const files = [1, 2, 3, 4].map(
      (i) => new File(['hello'], `${i}.png`, { type: 'image/png' })
    )
    await expect(
      requestImageGeneration({
        fullKey: 'fixture',
        model: 'grok-imagine-image-2.0-console',
        prompt: 'Edit this',
        count: 1,
        size: 'auto',
        quality: 'low',
        style: '',
        images: files,
      })
    ).rejects.toThrow('Too many reference images for this model')
    expect(postMock).not.toHaveBeenCalled()
  })

  test('an aborted Console image read never sends an upstream request', async () => {
    const controller = new AbortController()
    controller.abort()
    await expect(
      requestImageGeneration({
        fullKey: 'fixture',
        model: 'grok-imagine-image-2.0-console',
        prompt: 'Edit this',
        count: 1,
        size: 'auto',
        quality: 'low',
        style: '',
        images: [new File(['hello'], 'a.png', { type: 'image/png' })],
        signal: controller.signal,
      })
    ).rejects.toMatchObject({ name: 'AbortError' })
    expect(postMock).not.toHaveBeenCalled()
  })

  test('cancelling during Console file conversion stops before submitting', async () => {
    const controller = new AbortController()
    const request = requestImageGeneration({
      fullKey: 'fixture',
      model: 'grok-imagine-image-2.0-console',
      prompt: 'Edit this',
      count: 1,
      size: 'auto',
      quality: 'low',
      style: '',
      images: [new File(['hello'], 'a.png', { type: 'image/png' })],
      signal: controller.signal,
    })
    controller.abort()
    await expect(request).rejects.toMatchObject({ name: 'AbortError' })
    expect(postMock).not.toHaveBeenCalled()
  })

  test('Console file read errors are reported without sending a request', async () => {
    vi.spyOn(FileReader.prototype, 'readAsDataURL').mockImplementation(
      function (this: FileReader) {
        this.dispatchEvent(new ProgressEvent('error'))
        this.dispatchEvent(new ProgressEvent('loadend'))
      }
    )
    await expect(
      requestImageGeneration({
        fullKey: 'fixture',
        model: 'grok-imagine-image-2.0-console',
        prompt: 'Edit this',
        count: 1,
        size: 'auto',
        quality: 'low',
        style: '',
        images: [new File(['hello'], 'a.png', { type: 'image/png' })],
      })
    ).rejects.toThrow('Failed to read reference image')
    expect(postMock).not.toHaveBeenCalled()
  })

  test('Console infers a known image MIME type when the uploaded file omits it', async () => {
    postMock.mockResolvedValue({ data: { data: [] } })
    await requestImageGeneration({
      fullKey: 'fixture',
      model: 'grok-imagine-image-2.0-console',
      prompt: 'Edit this',
      count: 1,
      size: 'auto',
      quality: 'low',
      style: '',
      images: [new File(['hello'], 'a.webp')],
    })
    expect(postMock.mock.calls[0][1]).toMatchObject({
      images: [{ url: 'data:image/webp;base64,aGVsbG8=' }],
    })
  })
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
        skipAuthRefresh: true,
        preserveAuthorization: true,
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
      skipAuthRefresh: true,
      preserveAuthorization: true,
    })
    expect(images[0]).toMatchObject({
      source: 'b64',
      width: 1024,
      height: 1024,
    })
  })
})
