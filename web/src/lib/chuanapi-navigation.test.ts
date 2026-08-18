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
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, test } from 'vitest'

import { CHUANAPI_PUBLIC_HOME } from './chuanapi-navigation'

const webSrc = resolve(dirname(fileURLToPath(import.meta.url)), '..')

function source(path: string) {
  return readFileSync(resolve(webSrc, path), 'utf8')
}

describe('ChuanAPI cross-shell navigation', () => {
  test('uses the canonical public homepage', () => {
    expect(CHUANAPI_PUBLIC_HOME).toBe('https://chuanapi.com/')
  })

  test('marks Home as a same-tab document navigation', () => {
    const links = source('hooks/use-top-nav-links.ts')
    expect(links).toMatch(/href: CHUANAPI_PUBLIC_HOME/)
    expect(links).toMatch(/documentNavigation: true/)
  })

  test('keeps New API pricing navigation inside the SPA', () => {
    const links = source('hooks/use-top-nav-links.ts')
    const pricingBlock = links.match(
      /links\.push\(\{\s*title: t\('Model Square'\),[\s\S]*?\}\)/
    )?.[0]

    expect(pricingBlock).toContain("href: '/pricing'")
    expect(pricingBlock).not.toContain('documentNavigation')
  })

  test('renders pricing routes without self-reloading the document', () => {
    const pricingRoute = source('routes/pricing/index.tsx')
    const modelRoute = source('routes/pricing/$modelId/index.tsx')

    expect(pricingRoute).toMatch(/component: Pricing/)
    expect(modelRoute).toMatch(/component: ModelDetails/)
    expect(pricingRoute).not.toMatch(/reloadDocument/)
    expect(modelRoute).not.toMatch(/reloadDocument/)
  })

  test('brand links bypass the New API SPA router', () => {
    const brand = source('components/layout/components/system-brand.tsx')
    expect(brand).toMatch(/<a[\s\S]*href=\{CHUANAPI_PUBLIC_HOME\}/)
    expect(brand).not.toMatch(/<Link[\s\S]*CHUANAPI_PUBLIC_HOME/)
  })
})
