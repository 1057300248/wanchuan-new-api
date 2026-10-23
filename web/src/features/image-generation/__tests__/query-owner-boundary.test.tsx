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
import { QueryClientProvider, type QueryClient } from '@tanstack/react-query'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { AxiosError, type InternalAxiosRequestConfig } from 'axios'
import type { ReactNode } from 'react'
import { toast } from 'sonner'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import {
  applyAuthBundle,
  clearAuthenticatedClientState,
  clearAuthentication,
} from '@/lib/auth-session'
import { createAppQueryClient } from '@/lib/query-client'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import { useImageData } from '../hooks/use-image-data'

const originalAdapter = api.defaults.adapter
let client: QueryClient
let requests: string[]
let revision: number

function bundle(id = 101): AuthBundle {
  const now = Math.floor(Date.now() / 1000)
  return {
    access_token: `synthetic-image-access-${id}`,
    token_type: 'Bearer',
    access_expires_at: now + 600,
    user: { id, username: `synthetic-image-user-${id}`, role: 1 },
    session: {
      sid: `synthetic-image-session-${id}`, current: true,
      login_method: 'password', ip: '127.0.0.1', user_agent: 'test',
      created_at: now, last_active_at: now, expires_at: now + 3600,
    },
  }
}

function response(config: InternalAxiosRequestConfig, data: unknown) {
  return { config, data, status: 200, statusText: 'OK', headers: {} }
}

function unauthorized(config: InternalAxiosRequestConfig) {
  return new AxiosError('expired', 'ERR_BAD_REQUEST', config, undefined, {
    config, data: { code: 'AUTH_ACCESS_EXPIRED' }, status: 401,
    statusText: 'Unauthorized', headers: {},
  })
}

beforeEach(() => {
  requests = []
  revision = 1
  client = createAppQueryClient()
  client.setDefaultOptions({ queries: { retry: false, staleTime: Infinity } })
  applyAuthBundle(bundle(), false)
  api.defaults.adapter = async (config) => {
    const url = config.url ?? ''
    requests.push(url)
    if (!useAuthStore.getState().auth.user) throw unauthorized(config)
    const model = revision === 1 ? 'dall-e-3' : 'gpt-image-2'
    if (url.startsWith('/api/token/?')) {
      return response(config, { success: true, data: { total: 1, items: [{
        id: 7, name: `revision-${revision}`, status: 1, group: 'default',
        model_limits_enabled: false, model_limits: '',
      }] } })
    }
    if (url === '/api/pricing') {
      return response(config, { success: true, data: [{ model_name: model,
        supported_endpoint_types: ['image-generation'] }] })
    }
    if (url === '/api/token/7/models') {
      return response(config, { success: true, data: [model] })
    }
    throw new Error(`Unexpected image request: ${url}`)
  }
  vi.spyOn(toast, 'error').mockImplementation(() => 'test-toast')
  vi.spyOn(XMLHttpRequest.prototype, 'send').mockImplementation(() => {
    throw new Error('Unexpected refresh request')
  })
})

afterEach(async () => {
  cleanup()
  await client.cancelQueries()
  client.clear()
  api.defaults.adapter = originalAdapter
  clearAuthentication(false)
  window.history.replaceState({}, '', '/')
  vi.restoreAllMocks()
})

function mount() {
  return renderHook(() => useImageData(), {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  })
}

test('explicit sign-out starts no more token, pricing or refresh requests while the image hook remains mounted', async () => {
  const { result } = mount()
  await waitFor(() => expect(result.current.availableModels).toEqual(['dall-e-3']))
  const staleBootstrapRetry = result.current.refetchBootstrap
  const staleTokenRetry = result.current.refetchTokenModels
  const beforeLogout = requests.length
  await act(async () => {
    clearAuthenticatedClientState(client, false)
    await Promise.allSettled([staleBootstrapRetry(), staleTokenRetry()])
    await client.invalidateQueries()
  })
  expect(requests.slice(beforeLogout)).toEqual([])
  expect(XMLHttpRequest.prototype.send).not.toHaveBeenCalled()
  expect(toast.error).not.toHaveBeenCalled()
  expect(result.current.keys).toEqual([])
  expect(result.current.availableModels).toEqual([])
})

test('same-account re-login never restores the previous session bootstrap or token-model cache', async () => {
  const { result } = mount()
  await waitFor(() => expect(result.current.availableModels).toEqual(['dall-e-3']))
  // Intentionally leave the old query cache present to verify the owner key,
  // independently of the normal sign-out helper clearing the query client.
  act(() => clearAuthentication(false))
  revision = 2
  act(() => applyAuthBundle(bundle(), false))
  await waitFor(() => expect(result.current.keys[0]?.name).toBe('revision-2'))
  expect(result.current.availableModels).not.toContain('dall-e-3')
  await waitFor(() => expect(result.current.availableModels).toEqual(['gpt-image-2']))
  expect(requests.filter((url) => url === '/api/pricing')).toHaveLength(2)
})

test('an anonymous image hook does not start or manually refetch protected data', async () => {
  clearAuthentication(false)
  const { result } = mount()
  await act(async () => {
    await Promise.allSettled([result.current.refetchBootstrap(), result.current.refetchTokenModels()])
  })
  expect(requests).toEqual([])
  expect(XMLHttpRequest.prototype.send).not.toHaveBeenCalled()
  expect(toast.error).not.toHaveBeenCalled()
})

test('a real signed-in session expiry still uses the official refresh and expiry notification', async () => {
  window.history.replaceState({}, '', '/sign-in')
  api.defaults.adapter = async (config) => { throw unauthorized(config) }
  vi.mocked(XMLHttpRequest.prototype.send).mockImplementation(function (this: XMLHttpRequest) {
    Object.defineProperties(this, {
      status: { value: 401, configurable: true },
      statusText: { value: 'Unauthorized', configurable: true },
      responseText: { value: JSON.stringify({ success: false, code: 'AUTH_REFRESH_EXPIRED' }), configurable: true },
      readyState: { value: 4, configurable: true },
    })
    queueMicrotask(() => this.onloadend?.(new ProgressEvent('loadend')))
  })
  mount()
  await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Session expired!'))
  expect(XMLHttpRequest.prototype.send).toHaveBeenCalledTimes(1)
  expect(useAuthStore.getState().auth.user).toBeNull()
})
