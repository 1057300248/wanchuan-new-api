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
import { describe, expect, it } from 'vitest'

import { isOAuthAvailableOnOrigin } from '../oauth'

describe('isOAuthAvailableOnOrigin', () => {
  it.each<[string, string, string, boolean]>([
    ['the same origin', 'https://chuanapi.com', 'https://chuanapi.com', true],
    [
      'a ServerAddress with path and query',
      'https://chuanapi.com/wallet?tab=topup',
      'https://chuanapi.com',
      true,
    ],
    [
      'the default https port',
      'https://chuanapi.com:443',
      'https://chuanapi.com',
      true,
    ],
    [
      'mixed-case host and scheme',
      'HTTPS://CHUANAPI.COM',
      'https://chuanapi.com',
      true,
    ],
    [
      'a local development origin',
      'http://localhost:3000',
      'http://localhost:3000',
      true,
    ],
    [
      'a sibling entry',
      'https://api.chuanapi.com',
      'https://chuanapi.com',
      false,
    ],
    ['the CN entry', 'https://cn.chuanapi.com', 'https://chuanapi.com', false],
    [
      'a different scheme',
      'http://chuanapi.com',
      'https://chuanapi.com',
      false,
    ],
    [
      'a different effective port',
      'http://localhost:3001',
      'http://localhost:3000',
      false,
    ],
  ])('returns %s availability', (_name, serverAddress, origin, expected) => {
    expect(isOAuthAvailableOnOrigin(serverAddress, origin)).toBe(expected)
  })

  it('keeps OAuth offered when status carries no usable ServerAddress', () => {
    expect(isOAuthAvailableOnOrigin(undefined, 'https://chuanapi.com')).toBe(
      true
    )
    expect(isOAuthAvailableOnOrigin(null, 'https://chuanapi.com')).toBe(true)
    expect(isOAuthAvailableOnOrigin('   ', 'https://chuanapi.com')).toBe(true)
    expect(isOAuthAvailableOnOrigin('not a url', 'https://chuanapi.com')).toBe(
      true
    )
  })
})
