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
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { runInNewContext } from 'node:vm'

import { createRsbuild } from '@rsbuild/core'
import { afterEach, describe, expect, it, vi } from 'vitest'

import productionConfig from '../../../rsbuild.config'

interface CompiledBuildMetadata {
  getBuildRevision(): string
}

afterEach(() => {
  vi.unstubAllEnvs()
})

describe('production build revision', () => {
  it.each([
    ['a supplied release revision', 'rc46-test-abc123', 'rc46-test-abc123'],
    ['an empty release revision', '', '0000'],
    ['an absent release revision', undefined, '0000'],
  ])(
    'preserves the expected revision for %s',
    async (_label, version, expected) => {
      vi.stubEnv('VITE_REACT_APP_VERSION', version)
      const directory = await mkdtemp(
        path.join(tmpdir(), 'newapi-build-metadata-')
      )
      try {
        // Use the real production define configuration and real module. Vitest's
        // import.meta.env emulation alone cannot detect Rspack substitution bugs.
        const config = productionConfig({
          env: 'production',
          envMode: 'production',
          command: 'build',
        })
        const rsbuild = await createRsbuild({
          cwd: directory,
          config: {
            mode: 'production',
            source: {
              entry: {
                metadata: path.resolve('src/lib/build-metadata.ts'),
              },
              define: config.source?.define,
            },
            output: {
              target: 'web',
              distPath: { root: directory, js: '' },
              filename: { js: 'metadata.js' },
              cleanDistPath: false,
              minify: true,
            },
            performance: { buildCache: false, printFileSize: false },
            tools: {
              rspack: {
                output: { library: { name: 'BuildMetadata', type: 'var' } },
              },
            },
          },
        })
        const result = await rsbuild.build()
        try {
          const runtime: { BuildMetadata?: CompiledBuildMetadata } = {}
          runInNewContext(
            await readFile(path.join(directory, 'metadata.js'), 'utf8'),
            runtime
          )
          expect(runtime.BuildMetadata?.getBuildRevision()).toBe(
            `rv.${expected}.2k6e8r7p`
          )
        } finally {
          await result.close()
        }
      } finally {
        await rm(directory, { recursive: true, force: true })
      }
    }
  )
})
