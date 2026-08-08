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

import {
  isValidPositiveInt,
  parseBonusEligible,
  parseMetadataArray,
  parsePositiveInt,
  serializeBonusEligible,
  serializeMetadataArray,
} from '../model-metadata'

describe('parseMetadataArray', () => {
  test('parses a valid JSON array string into items', () => {
    assert.deepEqual(parseMetadataArray('["text","image"]'), [
      'text',
      'image',
    ])
  })

  test('returns empty array for empty, null or undefined input', () => {
    assert.deepEqual(parseMetadataArray(''), [])
    assert.deepEqual(parseMetadataArray(null), [])
    assert.deepEqual(parseMetadataArray(undefined), [])
  })

  test('degrades invalid JSON to an empty array', () => {
    assert.deepEqual(parseMetadataArray('not-json'), [])
  })

  test('degrades non-array JSON to an empty array', () => {
    assert.deepEqual(parseMetadataArray('{"text":true}'), [])
  })

  test('trims, deduplicates and filters blank/non-string entries', () => {
    assert.deepEqual(parseMetadataArray('[" text ","  ","vision",42,"text"]'), [
      'text',
      'vision',
    ])
  })
})

describe('serializeMetadataArray', () => {
  test('serializes items into a JSON array string', () => {
    assert.equal(serializeMetadataArray(['text', 'image']), '["text","image"]')
  })

  test('returns empty string for an empty array', () => {
    assert.equal(serializeMetadataArray([]), '')
  })

  test('trims, deduplicates and drops blank entries before serializing', () => {
    assert.equal(
      serializeMetadataArray([' text ', ' ', 'vision', 'text']),
      '["text","vision"]'
    )
  })
})

describe('parsePositiveInt', () => {
  test('parses a positive integer', () => {
    assert.equal(parsePositiveInt('128000'), 128000)
  })

  test('trims surrounding whitespace', () => {
    assert.equal(parsePositiveInt(' 128 '), 128)
  })

  test('returns null for empty input', () => {
    assert.equal(parsePositiveInt(''), null)
  })

  test('rejects zero, negatives and non-numeric input', () => {
    assert.equal(parsePositiveInt('0'), null)
    assert.equal(parsePositiveInt('-5'), null)
    assert.equal(parsePositiveInt('abc'), null)
    assert.equal(parsePositiveInt('12.5'), null)
    assert.equal(parsePositiveInt('9007199254740992'), null)
  })
})

describe('isValidPositiveInt', () => {
  test('accepts a positive integer or empty value', () => {
    assert.equal(isValidPositiveInt('128000'), true)
    assert.equal(isValidPositiveInt(''), true)
    assert.equal(isValidPositiveInt('   '), true)
  })

  test('rejects zero, negatives and non-numeric input', () => {
    assert.equal(isValidPositiveInt('0'), false)
    assert.equal(isValidPositiveInt('-5'), false)
    assert.equal(isValidPositiveInt('abc'), false)
  })
})

describe('parseBonusEligible', () => {
  test('maps true, false and null to the tri-state without collapsing null', () => {
    assert.equal(parseBonusEligible(true), 'eligible')
    assert.equal(parseBonusEligible(false), 'ineligible')
    assert.equal(parseBonusEligible(null), 'unset')
    assert.equal(parseBonusEligible(undefined), 'unset')
  })
})

describe('serializeBonusEligible', () => {
  test('round-trips the tri-state back to API boolean values', () => {
    assert.equal(serializeBonusEligible('eligible'), true)
    assert.equal(serializeBonusEligible('ineligible'), false)
    assert.equal(serializeBonusEligible('unset'), null)
  })

  test('never serializes numeric 1/0 values', () => {
    assert.notEqual(serializeBonusEligible('eligible'), 1)
    assert.notEqual(serializeBonusEligible('ineligible'), 0)
  })
})
