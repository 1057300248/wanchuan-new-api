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
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, test, vi } from 'vitest'

import { ModelMappingEditor } from '../model-mapping-editor'

describe('model mapping editor external value sync', () => {
  test('parses an external JSON value into visual rows once', () => {
    const onChange = vi.fn()
    const { rerender } = render(
      <ModelMappingEditor
        value={JSON.stringify({ 'gpt-3.5-turbo': 'gpt-4' })}
        onChange={onChange}
      />
    )

    expect(screen.getByDisplayValue('gpt-3.5-turbo')).toBeInTheDocument()
    expect(screen.getByDisplayValue('gpt-4')).toBeInTheDocument()

    rerender(
      <ModelMappingEditor
        value={JSON.stringify({ 'gpt-3.5-turbo': 'gpt-4' })}
        onChange={onChange}
      />
    )

    expect(screen.getAllByDisplayValue('gpt-3.5-turbo')).toHaveLength(1)
  })

  test('shows a validation error for invalid JSON without looping updates', () => {
    const onChange = vi.fn()
    render(<ModelMappingEditor value='not-json' onChange={onChange} />)

    expect(
      screen.getByText('Model mapping must be valid JSON format')
    ).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  test('propagates visual row edits as JSON', () => {
    const onChange = vi.fn()
    render(
      <ModelMappingEditor
        value={JSON.stringify({ a: 'b' })}
        onChange={onChange}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: 'Add Mapping' }))

    expect(onChange).toHaveBeenCalledTimes(1)
    expect(typeof onChange.mock.calls[0][0]).toBe('string')
  })
})
