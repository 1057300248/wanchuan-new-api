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
export interface OverviewPanelFlags {
  apiInfo: boolean
  announcements: boolean
  faq: boolean
  uptimeKuma: boolean
}

/**
 * Decide which info panels the dashboard overview may render for the
 * signed-in user.
 *
 * Admins keep every panel (performance health is additionally rendered
 * by the caller). Regular users get a customer-first overview: only
 * announcements are shown; API info, FAQ and uptime/monitoring panels
 * are dropped so the first screen stays focused on real usage data.
 */
export function getOverviewPanelVisibility(
  flags: OverviewPanelFlags,
  isAdmin: boolean
): OverviewPanelFlags {
  if (isAdmin) return flags
  return {
    apiInfo: false,
    announcements: flags.announcements,
    faq: false,
    uptimeKuma: false,
  }
}
