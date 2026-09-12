import { describe, expect, test } from 'vitest'

import { formatCoinAmount, parseCoinAmount } from '../coin-amount'

describe('Coin amount precision', () => {
  test.each([
    ['1', 1000000],
    ['0.5', 500000],
    ['0.000002', 2],
    ['3000', 3000000000],
    ['9007199254.740991', Number.MAX_SAFE_INTEGER],
    ['0', null],
    ['-1', null],
    ['1.0000001', null],
    ['1.5coins', null],
    ['1e6', null],
    ['9007199254.740992', null],
  ])('parses displayed %s into exact microcoins', (value, expected) => {
    expect(parseCoinAmount(value, 1000000)).toBe(expected)
  })

  test('formats wallet-range amounts without losing the final microcoin', () => {
    expect(formatCoinAmount(3000000000, 1000000)).toBe('3000.000000')
    expect(formatCoinAmount(Number.MAX_SAFE_INTEGER, 1000000)).toBe(
      '9007199254.740991'
    )
    expect(formatCoinAmount(1, 1000000)).toBe('0.000001')
  })
})
