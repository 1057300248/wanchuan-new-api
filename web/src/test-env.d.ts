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
/**
 * Ambient types for `bun:test` used only by component tests.
 *
 * The project runs tests with `bun test` (the `tsconfig` `types` list does
 * not include Bun), so the `mock` helper used for module-boundary mocks is
 * declared here; the runtime implementation comes from Bun itself.
 */
declare module 'bun:test' {
  export namespace mock {
    export function module(module: string, factory: () => unknown): void
  }
  export function mock(module: string, factory: () => unknown): void
}
