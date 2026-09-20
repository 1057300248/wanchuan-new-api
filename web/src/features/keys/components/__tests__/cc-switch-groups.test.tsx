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
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import type { ApiKey } from '../../types'
import { CCSwitchDialog } from '../dialogs/cc-switch-dialog'
import {
  buildValidatedCCSwitchURL,
  ccSwitchModelsQueryKey,
} from '../dialogs/cc-switch-utils'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  open: vi.fn(),
  warning: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: { ...actual.api, get: mocks.get, post: mocks.post, put: mocks.put },
  }
})

vi.mock('sonner', () => ({ toast: { warning: mocks.warning } }))

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})

const token: ApiKey = {
  id: 7,
  name: 'restricted',
  key: 'masked',
  status: 1,
  remain_quota: 123,
  used_quota: 0,
  unlimited_quota: false,
  expired_time: -1,
  created_time: 1,
  accessed_time: 1,
  group: 'auto',
  auto_groups: ['a', 'b'],
  cross_group_retry: true,
  model_limits_enabled: true,
  model_limits: 'a-only,b-only,codex-b',
  allow_ips: '',
}

let client: QueryClient

function view(currentToken = token, open = true) {
  return (
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <CCSwitchDialog
          open={open}
          onOpenChange={vi.fn()}
          token={currentToken}
          tokenKey='sk-testkey'
        />
      </QueryClientProvider>
    </I18nextProvider>
  )
}

function responseFor(path: string) {
  const url = new URL(path, 'https://example.invalid')
  const group = url.searchParams.get('group')
  const app = url.searchParams.get('app')
  const data =
    group === 'b'
      ? [app === 'codex' ? 'codex-b' : 'b-only']
      : ['a-only', 'b-only']
  return { data: { success: true, data, groups: ['a', 'b'] } }
}

beforeEach(() => {
  vi.clearAllMocks()
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  mocks.get.mockImplementation(async (path: string) => responseFor(path))
  vi.spyOn(window, 'open').mockImplementation(mocks.open)
})

afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
})

async function selectGroup(
  user: ReturnType<typeof userEvent.setup>,
  group: string
) {
  const picker = await screen.findByLabelText('Group')
  await waitFor(() => expect(picker).not.toBeDisabled())
  await user.click(picker)
  await user.click(await screen.findByRole('option', { name: group }))
}

async function selectModel(
  user: ReturnType<typeof userEvent.setup>,
  model: string
) {
  const picker = screen.getByLabelText(/Primary Model/)
  await waitFor(() => expect(picker).not.toBeDisabled())
  await user.click(picker)
  await user.click(await screen.findByText(model))
}

it('renders real group choices, refreshes models and imports the selected route without writing the key', async () => {
  const user = userEvent.setup()
  const original = JSON.stringify(token)
  render(view())
  await waitFor(() =>
    expect(mocks.get).toHaveBeenCalledWith(
      '/api/user/tokens/7/models?app=claude'
    )
  )
  await selectModel(user, 'a-only')
  await selectGroup(user, 'b')
  await waitFor(() =>
    expect(mocks.get).toHaveBeenCalledWith(
      '/api/user/tokens/7/models?app=claude&group=b'
    )
  )
  expect(screen.getByLabelText(/Primary Model/)).toHaveValue('')
  await selectModel(user, 'b-only')
  await user.click(screen.getByRole('button', { name: 'Open CC Switch' }))
  await waitFor(() => expect(mocks.open).toHaveBeenCalledTimes(1))
  const imported = new URL(mocks.open.mock.calls[0][0])
  expect(imported.searchParams.get('apiKey')).toBe('sk-testkey-grp-62')
  expect(imported.searchParams.get('model')).toBe('b-only')
  expect(imported.searchParams.get('app')).toBe('claude')
  expect(mocks.post).not.toHaveBeenCalled()
  expect(mocks.put).not.toHaveBeenCalled()
  expect(JSON.stringify(token)).toBe(original)
})

it('keeps the selected group while switching applications and clears stale model choices', async () => {
  const user = userEvent.setup()
  render(view())
  await selectGroup(user, 'b')
  await selectModel(user, 'b-only')
  await user.click(screen.getByRole('radio', { name: 'Codex' }))
  await waitFor(() =>
    expect(mocks.get).toHaveBeenCalledWith(
      '/api/user/tokens/7/models?app=codex&group=b'
    )
  )
  expect(screen.getByLabelText(/Primary Model/)).toHaveValue('')
  await selectModel(user, 'codex-b')
  await user.click(screen.getByRole('button', { name: 'Open CC Switch' }))
  await waitFor(() => expect(mocks.open).toHaveBeenCalledTimes(1))
  const imported = new URL(mocks.open.mock.calls[0][0])
  expect(imported.searchParams.get('apiKey')).toBe('sk-testkey-grp-62')
  expect(imported.searchParams.get('endpoint')).toMatch(/\/v1$/)
})

it('rechecks group permission immediately before import, even when the model still exists', async () => {
  const user = userEvent.setup()
  render(view())
  await selectGroup(user, 'b')
  await selectModel(user, 'b-only')
  mocks.get.mockResolvedValueOnce({
    data: { success: true, data: ['b-only'], groups: ['a'] },
  })
  await user.click(screen.getByRole('button', { name: 'Open CC Switch' }))
  await waitFor(() => expect(mocks.warning).toHaveBeenCalled())
  expect(mocks.open).not.toHaveBeenCalled()
  expect(mocks.put).not.toHaveBeenCalled()
})

it('does not open a stale import after switching to a different API key', async () => {
  const user = userEvent.setup()
  const { rerender } = render(view())
  await selectModel(user, 'a-only')
  let resolve!: (value: ReturnType<typeof responseFor>) => void
  mocks.get.mockImplementationOnce(
    () =>
      new Promise<ReturnType<typeof responseFor>>((done) => {
        resolve = done
      })
  )
  await user.click(screen.getByRole('button', { name: 'Open CC Switch' }))
  await waitFor(() => expect(resolve).toBeTypeOf('function'))
  rerender(view({ ...token, id: 8, group: 'a', auto_groups: [] }))
  await act(async () => {
    resolve(responseFor('/api/user/tokens/7/models?app=claude'))
  })
  await waitFor(() =>
    expect(mocks.get).toHaveBeenCalledWith(
      '/api/user/tokens/8/models?app=claude'
    )
  )
  expect(mocks.open).not.toHaveBeenCalled()
  expect(screen.getByLabelText(/Primary Model/)).toHaveValue('')
})

it('retains the canonical API key when following its original routing settings', async () => {
  const user = userEvent.setup()
  render(view())
  await selectModel(user, 'a-only')
  await user.click(screen.getByRole('button', { name: 'Open CC Switch' }))
  await waitFor(() => expect(mocks.open).toHaveBeenCalledTimes(1))
  const imported = new URL(mocks.open.mock.calls[0][0])
  expect(imported.searchParams.get('apiKey')).toBe('sk-testkey')
})

it.each(['a', ''])(
  'does not offer a group override for a fixed-group key (%s)',
  async (group) => {
    const user = userEvent.setup()
    mocks.get.mockResolvedValue({
      data: { success: true, data: ['a-only'], groups: ['a'] },
    })
    render(view({ ...token, group, auto_groups: [] }))
    await waitFor(() =>
      expect(screen.getByLabelText(/Primary Model/)).not.toBeDisabled()
    )
    expect(screen.queryByLabelText('Group')).not.toBeInTheDocument()
    await selectModel(user, 'a-only')
    await user.click(screen.getByRole('button', { name: 'Open CC Switch' }))
    await waitFor(() => expect(mocks.open).toHaveBeenCalledTimes(1))
    expect(
      new URL(mocks.open.mock.calls[0][0]).searchParams.get('apiKey')
    ).toBe('sk-testkey')
    expect(
      mocks.get.mock.calls.every(([path]) => !path.includes('&group='))
    ).toBe(true)
  }
)

it('opens model options outside the clipping dialog and supports search and keyboard selection', async () => {
  const user = userEvent.setup()
  render(view())
  const picker = screen.getByLabelText(/Primary Model/)
  await waitFor(() => expect(picker).not.toBeDisabled())
  await user.click(picker)
  expect(picker).toHaveAttribute('aria-expanded', 'true')
  expect(await screen.findByRole('option', { name: 'a-only' })).toBeVisible()
  // The centered dialog clips overflow; model options must escape that surface.
  expect(screen.getByRole('dialog')).not.toContainElement(
    screen.getByRole('listbox')
  )
  await user.type(picker, 'b-only')
  expect(
    screen.queryByRole('option', { name: 'a-only' })
  ).not.toBeInTheDocument()
  await user.keyboard('{ArrowDown}{Enter}')
  await waitFor(() => expect(picker).toHaveValue('b-only'))
  expect(picker).toHaveAttribute('aria-expanded', 'false')
  expect(screen.getByRole('dialog')).toBeVisible()
})

it('fails closed when the server cannot load token capabilities', async () => {
  mocks.get.mockResolvedValue({
    data: { success: false, message: 'unavailable' },
  })
  render(view())
  await waitFor(() => expect(mocks.get).toHaveBeenCalled())
  expect(screen.getByRole('button', { name: 'Open CC Switch' })).toBeDisabled()
  expect(mocks.open).not.toHaveBeenCalled()
})

it('rejects unverified overrides and preserves Unicode group names losslessly', () => {
  const build = (group: string, groups: string[], key = 'sk-testkey') =>
    buildValidatedCCSwitchURL(
      'codex',
      'test',
      { model: 'm' },
      key,
      ['m'],
      group,
      groups
    )
  expect(build('b', [])).toBeNull()
  expect(build('b', ['a'])).toBeNull()
  expect(build('auto', ['auto'])).toBeNull()
  expect(build('b', ['b'], 'sk-testkey-123')).toBeNull()
  const group = '<国模>-稳定'
  const result = build(group, [group])
  if (!result) throw new Error('expected a CC Switch URL for a validated group')
  const key = new URL(result).searchParams.get('apiKey')
  if (!key) throw new Error('expected an imported API key')
  expect(key.startsWith('sk-testkey-grp-')).toBe(true)
  const encoded = key.slice('sk-testkey-grp-'.length)
  const pairs = encoded.match(/../g)
  if (!pairs) throw new Error('expected hex-encoded group bytes')
  const bytes = Uint8Array.from(pairs.map((part) => Number.parseInt(part, 16)))
  expect(new TextDecoder().decode(bytes)).toBe(group)
})

it('separates group query caches without putting the API key in query keys', () => {
  expect(ccSwitchModelsQueryKey(token, 'codex', 'a')).not.toEqual(
    ccSwitchModelsQueryKey(token, 'codex', 'b')
  )
  expect(
    JSON.stringify(ccSwitchModelsQueryKey(token, 'codex', 'b'))
  ).not.toContain('sk-testkey')
})
