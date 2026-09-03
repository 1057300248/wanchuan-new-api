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
import { describe, expect, test, vi } from 'vitest'

import { HistoryPanel } from '../components/HistoryPanel'
import type { ImageHistoryEntry } from '../types'

type PanelProps = Parameters<typeof HistoryPanel>[0]

function renderPanel(overrides: Partial<PanelProps> = {}) {
  const props: PanelProps = {
    entries: [],
    totalCount: 0,
    query: '',
    onQueryChange: vi.fn(),
    orientation: 'all',
    onOrientationChange: vi.fn(),
    onRemove: vi.fn(),
    onClear: vi.fn(),
    onKeepLocally: vi.fn(async () => undefined),
    resolveUrl: (_id, fallback) => fallback,
    ...overrides,
  }
  render(<HistoryPanel {...props} />)
  return props
}

function makeEntry(id: string): ImageHistoryEntry {
  const now = Date.now()
  return {
    id,
    prompt: `prompt ${id}`,
    model: 'dall-e-3',
    settings: {
      size: '1024x1024',
      quality: 'standard',
      style: 'vivid',
      count: 1,
    },
    createdAt: now,
    results: [
      {
        id: `${id}-r`,
        type: 'url',
        source: 'https://cdn.example/a.png',
        keptLocally: false,
        createdAt: now,
      },
    ],
  }
}

describe('history panel separates empty history from empty filters', () => {
  test('shows the first-run hint only when no history exists at all', () => {
    renderPanel({ entries: [], totalCount: 0 })

    expect(screen.getByText('No history yet.')).toBeInTheDocument()
    expect(screen.queryByText('No matching history.')).not.toBeInTheDocument()
  })

  test('shows a no-match hint when filters hide every existing entry', () => {
    renderPanel({ entries: [], totalCount: 3 })

    expect(screen.queryByText('No history yet.')).not.toBeInTheDocument()
    expect(screen.getByText('No matching history.')).toBeInTheDocument()
  })

  test('shows neither hint once entries are visible', () => {
    renderPanel({ entries: [makeEntry('e1')], totalCount: 1 })

    expect(screen.queryByText('No history yet.')).not.toBeInTheDocument()
    expect(screen.queryByText('No matching history.')).not.toBeInTheDocument()
    expect(screen.getByText('prompt e1')).toBeInTheDocument()
  })
})
