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

import { Window } from 'happy-dom'

import { ROLE } from '@/lib/roles'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLAnchorElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { mock } = await import('bun:test')

function MockLink(props: {
  to?: string
  params?: Record<string, string>
  className?: string
  children?: React.ReactNode
}) {
  const href = props.params
    ? props.to?.replaceAll(/\$(\w+)/g, (_match, key) => props.params?.[key] ?? '')
    : props.to

  return (
    <a href={href} className={props.className}>
      {props.children}
    </a>
  )
}

mock.module('@tanstack/react-router', () => ({ Link: MockLink }))

const { WorkspaceSwitch } = await import('../workspace-switch')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Admin Dashboard': 'Admin Dashboard',
        'Customer Center': 'Customer Center',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type RenderResult = { link: HTMLAnchorElement | null; text: string }

async function renderSwitch(
  role: number | undefined,
  target: 'admin' | 'customer'
): Promise<RenderResult> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () =>
    root.render(
      <I18nextProvider i18n={i18n}>
        <WorkspaceSwitch role={role} target={target} />
      </I18nextProvider>
    )
  )

  const link = container.querySelector<HTMLAnchorElement>('a')
  const result: RenderResult = {
    link,
    text: container.textContent ?? '',
  }

  await act(async () => root.unmount())
  container.remove()
  return result
}

describe('WorkspaceSwitch target=admin (customer center header)', () => {
  test('admin sees an "Admin Dashboard" link to /dashboard/models', async () => {
    const { link, text } = await renderSwitch(ROLE.ADMIN, 'admin')
    assert.ok(link)
    assert.equal(link.getAttribute('href'), '/dashboard/models')
    assert.equal(text, 'Admin Dashboard')
  })

  test('super admin sees the same admin dashboard link', async () => {
    const { link, text } = await renderSwitch(ROLE.SUPER_ADMIN, 'admin')
    assert.ok(link)
    assert.equal(link.getAttribute('href'), '/dashboard/models')
    assert.equal(text, 'Admin Dashboard')
  })

  test('regular user sees no switch so the admin console stays hidden', async () => {
    const { link, text } = await renderSwitch(ROLE.USER, 'admin')
    assert.equal(link, null)
    assert.equal(text, '')
  })
})

describe('WorkspaceSwitch target=customer (admin header)', () => {
  test('admin sees a "Customer Center" link to /dashboard/overview', async () => {
    const { link, text } = await renderSwitch(ROLE.ADMIN, 'customer')
    assert.ok(link)
    assert.equal(link.getAttribute('href'), '/dashboard/overview')
    assert.equal(text, 'Customer Center')
  })

  test('super admin sees the same customer center link', async () => {
    const { link, text } = await renderSwitch(ROLE.SUPER_ADMIN, 'customer')
    assert.ok(link)
    assert.equal(link.getAttribute('href'), '/dashboard/overview')
    assert.equal(text, 'Customer Center')
  })

  test('regular user sees no switch so the customer center stays hidden from the admin shell', async () => {
    const { link, text } = await renderSwitch(ROLE.USER, 'customer')
    assert.equal(link, null)
    assert.equal(text, '')
  })

  test('guest or missing role never renders either switch', async () => {
    assert.equal((await renderSwitch(ROLE.GUEST, 'admin')).link, null)
    assert.equal((await renderSwitch(ROLE.GUEST, 'customer')).link, null)
    assert.equal((await renderSwitch(undefined, 'admin')).link, null)
    assert.equal((await renderSwitch(undefined, 'customer')).link, null)
  })
})
