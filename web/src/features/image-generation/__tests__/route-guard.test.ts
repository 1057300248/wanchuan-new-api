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
import { isRedirect } from '@tanstack/react-router'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { getStatus } from '@/lib/api'
import {
  getFreshSidebarModuleEnabled,
  isSidebarModuleEnabled,
} from '@/lib/nav-modules'
import { Route as ImageRoute } from '@/routes/_authenticated/image/index'

vi.mock('@/lib/api', async (importOriginal) => {
  const original = await importOriginal<typeof import('@/lib/api')>()
  return { ...original, getStatus: vi.fn() }
})

const mockedGetStatus = vi.mocked(getStatus)

function writeStatus(value: unknown) {
  window.localStorage.setItem('status', JSON.stringify(value))
}

function sidebarStatus(imageEnabled: boolean) {
  return {
    SidebarModulesAdmin: JSON.stringify({
      chat: { enabled: true, playground: true, image: imageEnabled },
    }),
  }
}

async function callImageBeforeLoad(): Promise<unknown> {
  const beforeLoad = ImageRoute.options.beforeLoad as unknown as (
    ...args: never[]
  ) => unknown
  return beforeLoad()
}

beforeEach(() => {
  window.localStorage.clear()
  mockedGetStatus.mockReset()
})

afterEach(() => {
  window.localStorage.clear()
})

describe('image route gate uses fresh module access', () => {
  test('redirects to dashboard when fresh status disables chat.image even if cache is stale', async () => {
    writeStatus(sidebarStatus(true))
    expect(isSidebarModuleEnabled('chat', 'image')).toBe(true)
    mockedGetStatus.mockResolvedValue(sidebarStatus(false))

    let thrown: unknown
    try {
      await callImageBeforeLoad()
    } catch (error) {
      thrown = error
    }

    expect(mockedGetStatus).toHaveBeenCalledTimes(1)
    expect(isRedirect(thrown)).toBe(true)
    expect((thrown as { options?: { to?: string } }).options?.to).toBe(
      '/dashboard'
    )
  })

  test('allows /image when fresh status enables chat.image despite a disabled cache', async () => {
    writeStatus(sidebarStatus(false))
    mockedGetStatus.mockResolvedValue(sidebarStatus(true))

    await expect(callImageBeforeLoad()).resolves.toBeUndefined()
    expect(JSON.parse(window.localStorage.getItem('status') ?? '{}')).toEqual(
      sidebarStatus(true)
    )
  })

  test('redirects when cached status is corrupt and fresh fetch fails', async () => {
    window.localStorage.setItem('status', 'not-json{{{')
    mockedGetStatus.mockRejectedValue(new Error('network down'))

    let thrown: unknown
    try {
      await callImageBeforeLoad()
    } catch (error) {
      thrown = error
    }

    expect(isRedirect(thrown)).toBe(true)
    expect((thrown as { options?: { to?: string } }).options?.to).toBe(
      '/dashboard'
    )
  })

  test('redirects when no cached status exists and fresh fetch fails', async () => {
    mockedGetStatus.mockRejectedValue(new Error('network down'))

    let thrown: unknown
    try {
      await callImageBeforeLoad()
    } catch (error) {
      thrown = error
    }

    expect(isRedirect(thrown)).toBe(true)
    expect((thrown as { options?: { to?: string } }).options?.to).toBe(
      '/dashboard'
    )
  })

  test('falls back to cached disabled value when the status request fails', async () => {
    writeStatus(sidebarStatus(false))
    mockedGetStatus.mockRejectedValue(new Error('network down'))

    let thrown: unknown
    try {
      await callImageBeforeLoad()
    } catch (error) {
      thrown = error
    }

    expect(isRedirect(thrown)).toBe(true)
    expect((thrown as { options?: { to?: string } }).options?.to).toBe(
      '/dashboard'
    )
  })

  test('getFreshSidebarModuleEnabled refreshes the cache for the next cached read', async () => {
    writeStatus(sidebarStatus(true))
    mockedGetStatus.mockResolvedValue(sidebarStatus(false))

    expect(await getFreshSidebarModuleEnabled('chat', 'image')).toBe(false)
    expect(isSidebarModuleEnabled('chat', 'image')).toBe(false)
  })
})
