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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { SettingsPageProvider } from '@/features/system-settings/components/settings-page-context'
import { api } from '@/lib/api'

import en from '../../i18n/locales/en.json'
import { MonitorSettings } from '../settings'
import type { MonitorConfig } from '../types'

const groupID = `route-${'a'.repeat(32)}`
const saved: MonitorConfig = {
  enabled: true,
  title: '',
  description: '',
  groups: { [groupID]: 'Premium' },
}

function renderSettings(config = saved, targetsFail = false) {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url.endsWith('/targets')) {
      if (targetsFail) throw new Error('offline')
      return {
        data: {
          success: true,
          data: [{ id: groupID, number: 1, model: 'example-model' }],
        },
      }
    }
    return {
      data: { success: true, data: { config, environment_enabled: true } },
    }
  })
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  const actions = document.createElement('div')
  document.body.append(actions)
  return render(
    <QueryClientProvider client={client}>
      <SettingsPageProvider actionsContainer={actions}>
        <MonitorSettings />
      </SettingsPageProvider>
    </QueryClientProvider>
  )
}

beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  await i18next.changeLanguage('en')
})

describe('Model monitor settings', () => {
  test('saves page text, visibility and group names together and uses the normalized saved values', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'put').mockImplementation(async (_url, value) => ({
      data: { success: true, data: value },
    }))
    renderSettings()
    const name = await screen.findByLabelText('Reference route 1 example-model')
    await user.clear(name)
    await user.type(name, 'VIP group')
    await user.type(screen.getByLabelText('Page title'), 'Our monitor')
    await user.click(
      screen.getByRole('switch', { name: 'Enable monitoring page' })
    )
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith('/api/option/model-monitor', {
        enabled: false,
        title: 'Our monitor',
        description: '',
        groups: { [groupID]: 'VIP group' },
      })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save Changes' })
      ).toBeDisabled()
    )
    expect(screen.getByRole('switch')).not.toBeChecked()
  })

  test('keeps saved group names editable when the current upstream catalog is unavailable', async () => {
    renderSettings(saved, true)
    expect(
      await screen.findByText('Unable to load the current groups')
    ).toBeVisible()
    expect(screen.getByLabelText('Previously saved group')).toHaveValue(
      'Premium'
    )
    expect(screen.getByLabelText('Page title')).toBeEnabled()
  })

  test('does not overwrite a dirty form after a failed save and can reset to saved values', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'put').mockResolvedValue({
      data: { success: false, message: 'monitor.settings.saveFailed' },
    })
    renderSettings()
    const input = await screen.findByLabelText('Page title')
    await user.type(input, 'Unsaved title')
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(api.put).toHaveBeenCalled())
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
    )
    expect(input).toHaveValue('Unsaved title')
    await user.click(screen.getByRole('button', { name: 'Reset' }))
    expect(input).toHaveValue('')
  })

  test('rejects overlong text without making a save request', async () => {
    vi.spyOn(api, 'put')
    renderSettings()
    const input = await screen.findByLabelText('Page title')
    fireEvent.change(input, { target: { value: 'x'.repeat(121) } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'))
    expect(api.put).not.toHaveBeenCalled()
  })
})
