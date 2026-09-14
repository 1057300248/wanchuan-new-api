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
import type { NavigationExtension } from '../navigation'

export const CHUANAPI_PUBLIC_HOME = 'https://chuanapi.com/'
export const CHUANAPI_QUICKSTART_DOCS = 'https://chuanapi.com/docs/quickstart/'

const LEGACY_QUICKSTART_URLS = new Set([
  'https://chuanapi.com/#quickstart',
  'https://chuanapi.com#quickstart',
])

export const chuanapiNavigationExtension: NavigationExtension = {
  id: 'chuanapi/navigation',
  publicHomeUrl: CHUANAPI_PUBLIC_HOME,
  quickstartDocsUrl: CHUANAPI_QUICKSTART_DOCS,
  apply(links) {
    return links.map((link) => {
      if (link.href === '/') {
        return {
          ...link,
          href: CHUANAPI_PUBLIC_HOME,
          documentNavigation: true,
        }
      }

      if (link.href === '/help' || LEGACY_QUICKSTART_URLS.has(link.href)) {
        return {
          ...link,
          href: CHUANAPI_QUICKSTART_DOCS,
          documentNavigation: true,
        }
      }

      return { ...link }
    })
  },
}
