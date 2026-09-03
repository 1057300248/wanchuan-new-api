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
import { describe, expect, test, vi } from 'vitest'

import { GenerationForm } from '../components/GenerationForm'

const baseValue = {
  prompt: 'a lake at sunrise',
  apiKeyId: 1,
  model: 'dall-e-3',
  size: '1024x1024',
  quality: 'standard',
  style: 'vivid',
  count: 1,
}

function renderForm(
  overrides: Partial<Parameters<typeof GenerationForm>[0]> = {}
) {
  return render(
    <GenerationForm
      value={baseValue}
      onChange={() => undefined}
      keys={[
        {
          id: 1,
          name: 'key-1',
          group: 'default',
          model_limits_enabled: false,
          model_limits: '',
          status: 1,
        },
      ]}
      models={['dall-e-3']}
      sizes={['1024x1024']}
      qualities={['standard', 'hd']}
      styles={['vivid', 'natural']}
      maxCount={1}
      maxReferenceImages={0}
      references={[]}
      referenceErrorKey={null}
      onAddReferenceFiles={() => undefined}
      onRemoveReference={() => undefined}
      isGenerating={false}
      formError={null}
      isInitializing={false}
      onGenerate={() => undefined}
      onCancel={() => undefined}
      {...overrides}
    />
  )
}

describe('generation form protects key interactions', () => {
  test('empty prompt disables Generate and marks the field invalid', () => {
    renderForm({ value: { ...baseValue, prompt: '   ' } })
    expect(screen.getByRole('button', { name: 'Generate' })).toBeDisabled()
    expect(screen.getByLabelText('Prompt')).toHaveAttribute(
      'aria-invalid',
      'true'
    )
  })

  test('failed generation shows an inline error alongside the toast path', () => {
    renderForm({ formError: 'Image generation failed' })
    expect(screen.getByText('Image generation failed')).toBeInTheDocument()
  })

  test('cancelling while generating calls onCancel instead of onGenerate', async () => {
    const user = userEvent.setup()
    const onCancel = vi.fn()
    const onGenerate = vi.fn()
    renderForm({ isGenerating: true, onCancel, onGenerate })
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onGenerate).not.toHaveBeenCalled()
  })

  test('prompt example chips fill the prompt field', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderForm({ value: { ...baseValue, prompt: '' }, onChange })
    await user.click(
      screen.getByRole('button', {
        name: 'A tranquil lake at sunrise, soft mist, photorealistic',
      })
    )
    expect(onChange).toHaveBeenCalledWith({
      prompt: 'A tranquil lake at sunrise, soft mist, photorealistic',
    })
  })

  test('only exposes counts allowed by the selected model', async () => {
    const user = userEvent.setup()
    renderForm({ maxCount: 1 })
    await user.click(screen.getByLabelText('Count'))
    expect(screen.getByRole('option', { name: '1' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: '2' })).not.toBeInTheDocument()
  })

  test('keeps API key selection available while group models load', () => {
    renderForm({ isInitializing: true })
    expect(screen.getByLabelText('API key')).toBeEnabled()
    expect(screen.getByLabelText('Model')).toBeDisabled()
  })
})
