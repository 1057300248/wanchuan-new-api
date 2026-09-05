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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, test } from 'vitest'

import { SearchContext } from '@/context/search-context'

import { Search } from '../search'

function SearchFixture() {
  const [open, setOpen] = useState(false)
  return (
    <SearchContext value={{ open, setOpen }}>
      <Search placeholder='Rechercher' />
      {open && <div role='status'>Search opened</div>}
    </SearchContext>
  )
}

describe('header search', () => {
  test('keeps a translated label accessible in an icon-sized mobile button', () => {
    render(<SearchFixture />)

    const button = screen.getByRole('button', { name: 'Rechercher' })
    expect(button).toHaveClass('size-8', 'flex-none')
    expect(screen.getByText('Rechercher')).toHaveClass('hidden', 'sm:inline')
    expect(button).toHaveClass('sm:w-40')
  })

  test.each(['click', 'keyboard'] as const)(
    'opens search with %s while the mobile label is hidden',
    async (interaction) => {
      const user = userEvent.setup()
      render(<SearchFixture />)

      const button = screen.getByRole('button', { name: 'Rechercher' })
      if (interaction === 'click') {
        await user.click(button)
      } else {
        await user.tab()
        expect(button).toHaveFocus()
        await user.keyboard('{Enter}')
      }

      expect(screen.getByRole('status')).toHaveTextContent('Search opened')
    }
  )
})
