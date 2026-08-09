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
  aggregateOverviewTopModels,
  aggregateOverviewTrend,
  buildOverviewTimeWindow,
} from '../overview-metrics.ts'

describe('overview metrics', () => {
  test('builds exact 24h/7d/30d windows', () => {
    const now = 2_000_000_000
    assert.deepEqual(buildOverviewTimeWindow(1, now), {
      start: now - 86400,
      end: now,
    })
    assert.equal(buildOverviewTimeWindow(7, now).start, now - 7 * 86400)
    assert.equal(buildOverviewTimeWindow(30, now).start, now - 30 * 86400)
  })

  test('aggregates and ranks top models by spend', () => {
    const rows = [
      { model_name: 'model-a', quota: 30, count: 2, token_used: 10 },
      { model_name: 'model-b', quota: 50, count: 1, token_used: 20 },
      { model_name: 'model-a', quota: 25, count: 3, token_used: 15 },
    ]
    assert.deepEqual(aggregateOverviewTopModels(rows as never), [
      { name: 'model-a', quota: 55, requests: 5, tokens: 25 },
      { name: 'model-b', quota: 50, requests: 1, tokens: 20 },
    ])
  })

  test('merges equal timestamps and sorts the usage trend', () => {
    const rows = [
      { created_at: 20, quota: 2, count: 1, token_used: 5 },
      { created_at: 10, quota: 3, count: 2, token_used: 7 },
      { created_at: 20, quota: 4, count: 3, token_used: 11 },
      { created_at: 0, quota: 100, count: 100, token_used: 100 },
    ]
    assert.deepEqual(aggregateOverviewTrend(rows as never), [
      { timestamp: 10, quota: 3, requests: 2, tokens: 7 },
      { timestamp: 20, quota: 6, requests: 4, tokens: 16 },
    ])
  })
})
