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
import { BookOpen, KeyRound, Terminal, Wallet } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'

import type { ReactNode } from 'react'

const CURL_EXAMPLE = `curl https://chuanapi.com/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer sk-xxxx" \\
  -d '{
    "model": "gpt-5.2",
    "messages": [
      {"role": "user", "content": "Hello!"}
    ]
  }'`

const ENDPOINTS = [
  { method: 'GET', path: '/v1/models' },
  { method: 'POST', path: '/v1/chat/completions' },
  { method: 'POST', path: '/v1/embeddings' },
  { method: 'POST', path: '/v1/images/generations' },
  { method: 'POST', path: '/v1/audio/transcriptions' },
  { method: 'POST', path: '/v1/messages' },
] as const

function Section(props: {
  icon: ReactNode
  title: string
  children: ReactNode
}) {
  return (
    <section className='space-y-3'>
      <h2 className='text-foreground flex items-center gap-2 text-lg font-semibold'>
        <span className='text-primary'>{props.icon}</span>
        {props.title}
      </h2>
      <div className='text-muted-foreground space-y-3 text-sm leading-relaxed'>
        {props.children}
      </div>
    </section>
  )
}

function CodeBlock(props: { code: string }) {
  return (
    <pre className='bg-muted overflow-x-auto rounded-lg p-4 font-mono text-xs leading-relaxed'>
      <code>{props.code}</code>
    </pre>
  )
}

export function HelpDocs() {
  const { t } = useTranslation()
  const baseUrl = `${window.location.origin}/v1`

  return (
    <PublicLayout>
      <div className='mx-auto max-w-4xl space-y-10 px-4 py-10'>
        <header className='space-y-2'>
          <h1 className='text-foreground flex items-center gap-3 text-2xl font-bold'>
            <BookOpen className='text-primary size-7' />
            {t('API Documentation')}
          </h1>
          <p className='text-muted-foreground text-sm'>
            {t(
              'An OpenAI-compatible endpoint for chat, embeddings, images and audio models.'
            )}
          </p>
        </header>

        <Section icon={<Terminal className='size-5' />} title={t('Quick Start')}>
          <p>
            {t('Base URL:')}{' '}
            <code className='bg-muted rounded px-1.5 py-0.5 font-mono text-xs'>
              {baseUrl}
            </code>
          </p>
          <p>
            {t(
              'Create an API key in the console, then authenticate with the Authorization header.'
            )}
          </p>
          <a
            href='/keys'
            className='text-primary hover:underline'
          >
            {t('Go to API keys')} →          </a>
        </Section>

        <Section icon={<KeyRound className='size-5' />} title={t('Authentication')}>
          <p>
            {t(
              'Send your key as a Bearer token in the Authorization header of every request.'
            )}
          </p>
          <CodeBlock code='Authorization: Bearer sk-xxxx' />
        </Section>

        <Section icon={<Terminal className='size-5' />} title={t('Common Endpoints')}>
          <ul className='space-y-2'>
            {ENDPOINTS.map((endpoint) => (
              <li
                key={endpoint.path}
                className='flex items-center gap-3'
              >
                <span
                  className={`rounded px-1.5 py-0.5 font-mono text-[11px] font-semibold ${
                    endpoint.method === 'GET'
                      ? 'bg-blue-500/10 text-blue-600 dark:text-blue-400'
                      : 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                  }`}
                >
                  {endpoint.method}
                </span>
                <code className='font-mono text-xs'>{endpoint.path}</code>
              </li>
            ))}
          </ul>
        </Section>

        <Section icon={<Terminal className='size-5' />} title={t('Request Example')}>
          <CodeBlock code={CURL_EXAMPLE} />
          <p>
            {t(
              'Streaming responses follow the OpenAI SSE format with stream: true.'
            )}
          </p>
        </Section>

        <Section icon={<Wallet className='size-5' />} title={t('Billing')}>
          <p>
            {t(
              'Usage is prepaid and billed per token against your wallet balance. Check the wallet page for top-up and the usage logs page for request-level details.'
            )}
          </p>
          <div className='flex flex-wrap gap-4'>
            <a
              href='/wallet'
              className='text-primary hover:underline'
            >
              {t('Wallet')} →            </a>
            <a
              href='/usage-logs'
              className='text-primary hover:underline'
            >
              {t('Usage logs')} →            </a>
          </div>
        </Section>

        <footer className='text-muted-foreground/60 border-t pt-6 text-sm'>
          {t('More documentation is on the way.')}
        </footer>
      </div>
    </PublicLayout>
  )
}
