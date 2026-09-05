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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen } from '@testing-library/react'
import { beforeEach, expect, test, vi } from 'vitest'

import { ThemeCustomizationProvider } from '@/context/theme-customization-provider'
import { api } from '@/lib/api'
import { MARKETING_HOME_URL, QUICKSTART_DOCS_URL } from '@/lib/public-site'
import { useAuthStore } from '@/stores/auth-store'

import { useSidebarView } from '../use-sidebar-view'

function clearThemePresetCookie() {
  document.cookie =
    'theme_preset=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/'
}

function SidebarProbe() {
  const view = useSidebarView()
  const serializable = view.navGroups.map((group) => ({
    id: group.id,
    title: group.title,
    items: group.items.map((item) =>
      'url' in item
        ? {
            title: item.title,
            url: item.url,
            external: (item as { external?: boolean }).external ?? false,
          }
        : { title: item.title, url: '', external: false }
    ),
  }))
  return <div data-testid='sidebar-view'>{JSON.stringify(serializable)}</div>
}

function renderSidebarView() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={client}>
        <ThemeCustomizationProvider>
          <SidebarProbe />
        </ThemeCustomizationProvider>
      </QueryClientProvider>
    ),
  })
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return view
}

async function readSidebarGroups() {
  const node = await screen.findByTestId('sidebar-view')
  return JSON.parse(node.textContent ?? '[]') as Array<{
    id: string
    title: string
    items: Array<{ title: string; url: string; external: boolean }>
  }>
}

function urlsOf(items: Array<{ url?: string }>): string[] {
  return items
    .map((item) => ('url' in item ? String((item as { url: string }).url) : ''))
    .filter(Boolean)
}

beforeEach(() => {
  clearThemePresetCookie()
  window.localStorage.clear()
  vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
    if (url === '/api/status') return { data: { data: {} } }
    return { data: { success: true, data: [] } }
  })
})

test('wanchuan user view reorders after permission filtering with absolute marketing links', async () => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'user', role: 1 })
  renderSidebarView()

  const groups = await readSidebarGroups()
  expect(groups.length).toBeGreaterThan(0)
  expect(groups.find((group) => group.id === 'admin')).toBeUndefined()

  const workspace = groups[0]
  expect(workspace.id).toBe('general')
  const urls = urlsOf(workspace.items)
  expect(urls).toContain('/wallet')
  expect(urls).toEqual(
    [...urls].sort(
      (left, right) =>
        [
          '/dashboard/overview',
          '/keys',
          '/usage-logs/common',
          '/wallet',
          '/dashboard/models',
          '/usage-logs/task',
        ].indexOf(left) -
        [
          '/dashboard/overview',
          '/keys',
          '/usage-logs/common',
          '/wallet',
          '/dashboard/models',
          '/usage-logs/task',
        ].indexOf(right)
    )
  )

  const allItems = groups.flatMap((group) => group.items)
  const playground = allItems.find(
    (item) =>
      'url' in item &&
      String((item as { url: string }).url).includes('playground')
  ) as { url: string; external?: boolean } | undefined
  expect(playground?.url).toBe(`${MARKETING_HOME_URL}/playground/`)
  expect(playground?.external).toBe(true)

  const resources = groups.find((group) => group.id === 'resources')
  const resourceUrls = urlsOf(resources?.items ?? [])
  expect(resourceUrls).toContain(`${MARKETING_HOME_URL}/model-pricing/`)
  expect(resourceUrls).toContain(QUICKSTART_DOCS_URL)

  const personal = groups.find((group) => group.id === 'personal')
  expect(urlsOf(personal?.items ?? [])).not.toContain('/wallet')
})

test('wanchuan admin keeps the default navigation including the admin group', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 10 })
  renderSidebarView()

  const groups = await readSidebarGroups()
  expect(groups.length).toBeGreaterThan(0)
  expect(groups.find((group) => group.id === 'admin')).toBeDefined()
  expect(groups.find((group) => group.id === 'resources')).toBeUndefined()
  expect(groups[0].id).not.toBe('general')
})

test('default preset keeps internal routes without marketing rewrites', async () => {
  document.cookie = 'theme_preset=default; path=/'
  useAuthStore.getState().auth.setUser({ id: 7, username: 'user', role: 1 })
  renderSidebarView()

  const groups = await readSidebarGroups()
  expect(groups.length).toBeGreaterThan(0)
  expect(groups.find((group) => group.id === 'resources')).toBeUndefined()
  const allItems = groups.flatMap((group) => group.items)
  const playground = allItems.find(
    (item) => 'url' in item && (item as { url: string }).url === '/playground'
  ) as { url: string; external?: boolean } | undefined
  expect(playground).toBeDefined()
  expect(playground?.external).toBeFalsy()
})
