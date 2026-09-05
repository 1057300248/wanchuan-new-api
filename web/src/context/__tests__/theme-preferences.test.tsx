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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, afterEach, expect, it } from 'vitest'

import {
  ThemeCustomizationProvider,
  useThemeCustomization,
} from '../theme-customization-provider'

function Preferences() {
  const theme = useThemeCustomization()
  return (
    <>
      <span>{theme.customization.preset}</span>
      <button type='button' onClick={() => theme.setPreset('default')}>
        Original
      </button>
      <button type='button' onClick={() => theme.setPreset('wanchuan')}>
        WanChuan
      </button>
    </>
  )
}
beforeEach(() => {
  for (const key of ['theme_preset', 'theme_font', 'theme_radius']) {
    document.cookie = `${key}=; max-age=0; path=/`
  }
})
afterEach(() => {
  document.body.removeAttribute('data-theme-preset')
})
it('uses WanChuan for a fresh user and preserves explicit original theme after remount', async () => {
  const user = userEvent.setup()
  const view = render(
    <ThemeCustomizationProvider>
      <Preferences />
    </ThemeCustomizationProvider>
  )
  await waitFor(() =>
    expect(document.body).toHaveAttribute('data-theme-preset', 'wanchuan')
  )
  await user.click(screen.getByRole('button', { name: 'Original' }))
  expect(document.body).not.toHaveAttribute('data-theme-preset')
  view.unmount()
  render(
    <ThemeCustomizationProvider>
      <Preferences />
    </ThemeCustomizationProvider>
  )
  expect(screen.getByText('default')).toBeInTheDocument()
  expect(document.body).not.toHaveAttribute('data-theme-preset')
})
it('preserves stored palette, font and radius instead of overwriting preferences', () => {
  document.cookie = 'theme_preset=anthropic; path=/'
  document.cookie = 'theme_font=serif; path=/'
  document.cookie = 'theme_radius=sm; path=/'
  render(
    <ThemeCustomizationProvider>
      <Preferences />
    </ThemeCustomizationProvider>
  )
  expect(document.body).toHaveAttribute('data-theme-preset', 'anthropic')
  expect(document.body).toHaveAttribute('data-theme-font', 'serif')
  expect(document.body).toHaveAttribute('data-theme-radius', 'sm')
})
