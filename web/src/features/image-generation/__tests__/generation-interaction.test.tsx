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
  test('empty prompt disables Generate and shows validation only after leaving the input', async () => {
    renderForm({ value: { ...baseValue, prompt: '   ' } })
    expect(screen.getByRole('button', { name: 'Generate' })).toBeDisabled()
    expect(screen.getByLabelText('Prompt')).toHaveAttribute(
      'aria-invalid',
      'false'
    )
    const user = userEvent.setup()
    await user.click(screen.getByLabelText('Prompt'))
    await user.tab()
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
    await user.click(screen.getByRole('button', { name: 'Stop waiting' }))
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

  test('keeps API key selection visible in the toolbar while group models load', async () => {
    renderForm({ isInitializing: true })
    expect(screen.getByLabelText('Token')).toBeEnabled()
    expect(screen.getByLabelText('Model')).toBeDisabled()
  })
})

test('hides examples after input and submits with Control Enter', async () => {
  const onGenerate = vi.fn()
  renderForm({ onGenerate })
  expect(
    screen.queryByRole('button', {
      name: 'A tranquil lake at sunrise, soft mist, photorealistic',
    })
  ).not.toBeInTheDocument()
  const user = userEvent.setup()
  await user.click(screen.getByLabelText('Prompt'))
  await user.keyboard('{Control>}{Enter}{/Control}')
  expect(onGenerate).toHaveBeenCalledTimes(1)
})
test('does not generate with keyboard when no key or model is selected', async () => {
  const onGenerate = vi.fn()
  renderForm({ value: { ...baseValue, apiKeyId: null, model: '' }, onGenerate })
  const user = userEvent.setup()
  await user.click(screen.getByLabelText('Prompt'))
  await user.keyboard('{Control>}{Enter}{/Control}')
  expect(onGenerate).not.toHaveBeenCalled()
})

test('size and quality open option panels and apply selected values', async () => {
  const onChange = vi.fn()
  renderForm({ onChange })
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Size' }))
  expect(screen.getByRole('button', { name: '1024x1024' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Quality' }))
  await user.click(screen.getByRole('button', { name: 'HD' }))
  expect(onChange).toHaveBeenCalledWith({ quality: 'hd' })
})

test('long token and model labels stay clipped within non-shrinking toolbar controls', () => {
  renderForm({
    keys: [
      {
        id: 1,
        name: 'A very long token name for a shared image generation workspace',
        group: 'default',
        status: 1,
        model_limits_enabled: false,
        model_limits: '',
      },
    ],
    value: {
      ...baseValue,
      model: 'gpt-image-very-long-model-name-for-layout-regression',
    },
    models: ['gpt-image-very-long-model-name-for-layout-regression'],
  })
  for (const label of ['Token', 'Model']) {
    const trigger = screen.getByLabelText(label)
    expect(trigger).toHaveClass('min-w-0')
    expect(trigger.parentElement).toHaveClass('shrink-0')
    expect(trigger.querySelector('[data-slot="select-value"]')).toHaveClass(
      'min-w-0'
    )
    expect(
      trigger.querySelector('[data-slot="select-value"] > span')
    ).toHaveClass('truncate')
  }
})
