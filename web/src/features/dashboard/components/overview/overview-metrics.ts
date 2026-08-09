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
import type { FlowQuotaDataItem, QuotaDataItem } from '../../types'

export type OverviewRangeDays = 1 | 7 | 30

export interface OverviewTimeWindow {
  start: number
  end: number
}

export interface OverviewTopModel {
  name: string
  quota: number
  requests: number
  tokens: number
}

export interface OverviewTrendPoint {
  timestamp: number
  quota: number
  requests: number
  tokens: number
}

export function buildOverviewTimeWindow(
  days: OverviewRangeDays,
  nowSeconds = Math.floor(Date.now() / 1000)
): OverviewTimeWindow {
  return { start: nowSeconds - days * 86400, end: nowSeconds }
}

export function aggregateOverviewTopModels(
  rows: FlowQuotaDataItem[],
  limit = 5
): OverviewTopModel[] {
  const totals = new Map<string, OverviewTopModel>()
  for (const row of rows) {
    const name = row.model_name?.trim() || 'Unknown model'
    const current = totals.get(name) ?? {
      name,
      quota: 0,
      requests: 0,
      tokens: 0,
    }
    current.quota += Number(row.quota ?? 0)
    current.requests += Number(row.count ?? 0)
    current.tokens += Number(row.token_used ?? 0)
    totals.set(name, current)
  }
  return [...totals.values()]
    .sort((a, b) => b.quota - a.quota || b.requests - a.requests)
    .slice(0, Math.max(0, limit))
}

export function aggregateOverviewTrend(
  rows: QuotaDataItem[]
): OverviewTrendPoint[] {
  const totals = new Map<number, OverviewTrendPoint>()
  for (const row of rows) {
    const timestamp = Number(row.created_at ?? 0)
    if (!timestamp) continue
    const current = totals.get(timestamp) ?? {
      timestamp,
      quota: 0,
      requests: 0,
      tokens: 0,
    }
    current.quota += Number(row.quota ?? 0)
    current.requests += Number(row.count ?? 0)
    current.tokens += Number(row.token_used ?? 0)
    totals.set(timestamp, current)
  }
  return [...totals.values()].sort((a, b) => a.timestamp - b.timestamp)
}
