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
import { expect, test } from 'vitest'

import {
  applyGroupRenamesToValues,
  type GroupRenameEditableValues,
} from '../group-rename'

// Object.fromEntries deliberately creates own JSON keys, including __proto__;
// object-literal __proto__ syntax would invalidate this fixture itself.
function valuesFor(names: string[]): GroupRenameEditableValues {
  const keyed = (value: (name: string) => unknown) =>
    Object.fromEntries(names.map((name) => [name, value(name)]))
  return {
    GroupRatio: JSON.stringify(keyed(() => 1)),
    TopupGroupRatio: JSON.stringify(keyed(() => 0.95)),
    UserUsableGroups: JSON.stringify(keyed(() => 'Synthetic group')),
    GroupGroupRatio: JSON.stringify(keyed(() => keyed(() => 1.25))),
    AutoGroups: JSON.stringify(names),
    GroupSpecialUsableGroup: JSON.stringify(
      keyed(() => Object.fromEntries(names.flatMap((name) => [
        [name, 'visible'],
        [`+:${name}`, 'allowed'],
        [`-:${name}`, 'denied'],
      ])))
    ),
  }
}

test.each([
  ['vip', '__proto__', null],
  ['__proto__', 'gold', null],
  ['constructor', '__proto__', null],
  ['vip', 'gold', '__proto__'],
  ['vip', 'constructor', null],
  ['constructor', 'gold', null],
] as const)('preserves all own configuration keys when %s becomes %s (retained: %s)', (from, to, untouched) => {
  const names = untouched === null ? [from] : [from, untouched]
  const actual = applyGroupRenamesToValues(valuesFor(names), [{ from, to }])
  const expected = valuesFor(names.map((name) => name === from ? to : name))
  for (const field of Object.keys(expected) as (keyof GroupRenameEditableValues)[]) {
    expect(JSON.parse(actual[field]), field).toEqual(JSON.parse(expected[field]))
  }
  expect(Object.hasOwn(JSON.parse(actual.GroupRatio), to)).toBe(true)
  expect(Object.hasOwn(JSON.parse(actual.GroupGroupRatio), to)).toBe(true)
  expect(Object.hasOwn(JSON.parse(actual.GroupSpecialUsableGroup), to)).toBe(true)
})
