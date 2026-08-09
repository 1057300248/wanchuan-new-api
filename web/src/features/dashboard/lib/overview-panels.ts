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
 * Decide which info panels the dashboard overview may render.
 *
 * The overview lives inside the customer center, so every visitor
 * (including admins) gets the customer-first layout: only announcements
 * are shown; API info, FAQ and uptime/monitoring panels are dropped so
 * the first screen stays focused on real usage data. Admins reach the
 * performance/monitoring views from the admin dashboard instead.
 */
export function getOverviewPanelVisibility(
  flags: OverviewPanelFlags
): OverviewPanelFlags {
  return {
    apiInfo: false,
    announcements: flags.announcements,
    faq: false,
    uptimeKuma: false,
  }
}
