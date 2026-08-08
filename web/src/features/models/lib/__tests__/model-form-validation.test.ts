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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { modelFormSchema } from '../model-form'

// 除 model_name 外所有字段都有 default，最小合法输入即可通过校验
const validBase = {
  model_name: 'validation-test-model',
  context_length: '',
  max_output_tokens: '',
}

const INVALID_POSITIVE_INTS = [
  '0',
  '-5',
  '12.5',
  'abc',
  '1e3',
  '9007199254740992',
] as const

describe('modelFormSchema positive-integer metadata fields', () => {
  test('accepts empty and positive integer values', () => {
    assert.equal(
      modelFormSchema.safeParse(validBase).success,
      true,
      'empty values must pass'
    )
    assert.equal(
      modelFormSchema.safeParse({
        ...validBase,
        context_length: '128000',
        max_output_tokens: '16384',
      }).success,
      true,
      'positive integers must pass'
    )
  })

  test('rejects zero, negatives, decimals and letters for context_length', () => {
    for (const bad of INVALID_POSITIVE_INTS) {
      const result = modelFormSchema.safeParse({
        ...validBase,
        context_length: bad,
      })
      assert.equal(
        result.success,
        false,
        `context_length "${bad}" must be rejected`
      )
    }
  })

  test('rejects zero, negatives, decimals and letters for max_output_tokens', () => {
    for (const bad of INVALID_POSITIVE_INTS) {
      const result = modelFormSchema.safeParse({
        ...validBase,
        max_output_tokens: bad,
      })
      assert.equal(
        result.success,
        false,
        `max_output_tokens "${bad}" must be rejected`
      )
    }
  })

  test('reports the i18n key as the validation message', () => {
    const result = modelFormSchema.safeParse({
      ...validBase,
      context_length: '0',
    })
    assert.equal(result.success, false)
    if (!result.success) {
      assert.equal(
        result.error.issues[0].message,
        'Must be a positive integer or empty'
      )
    }
  })
})
