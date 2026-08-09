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
import { Link } from '@tanstack/react-router'
import {
  ArrowLeft,
  Code2,
  Gift,
  Layers3,
  ShieldCheck,
  WalletCards,
} from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'
import { useStatus } from '@/hooks/use-status'
import { useSystemConfig } from '@/hooks/use-system-config'
import { cn } from '@/lib/utils'

type AuthLayoutProps = {
  children: ReactNode
  mode?: 'sign-in' | 'sign-up'
  showSignUp?: boolean
}

export function AuthLayout({
  children,
  mode,
  showSignUp = true,
}: AuthLayoutProps) {
  const { t } = useTranslation()
  const { systemName, logo, loading } = useSystemConfig()
  const { status } = useStatus()

  if (!mode) {
    return (
      <div className='relative grid h-svh max-w-none'>
        <Link
          to='/'
          className='absolute top-4 left-4 z-10 flex items-center gap-2 transition-opacity hover:opacity-80 sm:top-8 sm:left-8'
        >
          <div className='relative h-8 w-8'>
            {loading ? (
              <Skeleton className='absolute inset-0 rounded-full' />
            ) : (
              <img
                src={logo}
                alt={t('Logo')}
                className='h-8 w-8 rounded-full object-cover'
              />
            )}
          </div>
          {loading ? (
            <Skeleton className='h-6 w-24' />
          ) : (
            <h1 className='text-xl font-medium'>{systemName}</h1>
          )}
        </Link>
        <div className='container flex items-center pt-16 sm:pt-0'>
          <div className='mx-auto flex w-full flex-col justify-center space-y-2 px-4 py-8 sm:w-[480px] sm:p-8'>
            {children}
          </div>
        </div>
      </div>
    )
  }

  const bonusEnabled =
    status?.signup_bonus_enabled ?? status?.data?.signup_bonus_enabled ?? false
  const bonusQuota =
    status?.signup_bonus_quota ?? status?.data?.signup_bonus_quota ?? 0
  const quotaPerUnit =
    status?.quota_per_unit ?? status?.data?.quota_per_unit ?? 0
  const signupBonus =
    bonusEnabled && bonusQuota > 0 && quotaPerUnit > 0
      ? (bonusQuota / quotaPerUnit).toLocaleString(undefined, {
          maximumFractionDigits: 2,
        })
      : null
  const modeLabel = mode === 'sign-in' ? t('Sign in') : t('Sign up')
  const modeTitle = mode === 'sign-in' ? t('Welcome back') : t('Create account')
  const switchPrompt =
    mode === 'sign-in'
      ? t("Don't have an account?")
      : t('Already have an account?')
  const switchLabel = mode === 'sign-in' ? t('Sign up') : t('Sign in')
  const switchTarget = mode === 'sign-in' ? '/sign-up' : '/sign-in'

  return (
    <main
      data-testid='auth-shell'
      className='dark:bg-background min-h-svh overflow-y-auto bg-[#f7f7f8]'
    >
      <div className='mx-auto flex min-h-svh w-full max-w-[1248px] items-center px-5 py-8 sm:px-8 lg:px-12'>
        <div className='grid w-full items-center gap-10 lg:grid-cols-[minmax(0,660px)_minmax(380px,460px)] lg:gap-8 xl:gap-16'>
          <aside
            data-testid='auth-marketing-panel'
            className='hidden min-w-0 lg:block'
          >
            <Link
              to='/'
              className='inline-flex items-center gap-3 transition-opacity hover:opacity-80'
            >
              <div className='relative h-10 w-10 shrink-0'>
                {loading ? (
                  <Skeleton className='absolute inset-0 rounded-xl' />
                ) : (
                  <img
                    src={logo}
                    alt={t('Logo')}
                    className='border-border/70 bg-background h-10 w-10 rounded-[8px] border object-cover shadow-sm'
                  />
                )}
              </div>
              {loading ? (
                <Skeleton className='h-7 w-32' />
              ) : (
                <span className='text-2xl font-semibold tracking-tight'>
                  {systemName}
                </span>
              )}
            </Link>

            <div className='mt-14'>
              <p className='text-muted-foreground text-sm font-medium'>
                {modeLabel}
              </p>
              <h1 className='mt-3 text-4xl font-semibold tracking-tight'>
                {modeTitle}
              </h1>
              <p className='text-muted-foreground mt-4 text-base'>
                {t('Multi-model AI API')}
              </p>
            </div>

            <div className='mt-10 grid max-w-[576px] grid-cols-2 gap-3'>
              <div className='border-border/70 bg-card min-h-24 rounded-[10px] border p-4 shadow-xs'>
                <Code2 className='text-muted-foreground bg-muted h-8 w-8 rounded-[8px] border p-1.5' />
                <p className='mt-4 text-sm leading-5'>
                  {t('Base URL')}: https://chuanapi.com/v1
                </p>
              </div>
              <div className='border-border/70 bg-card min-h-24 rounded-[10px] border p-4 shadow-xs'>
                <Layers3 className='text-muted-foreground bg-muted h-8 w-8 rounded-[8px] border p-1.5' />
                <p className='mt-4 text-sm leading-5'>
                  OpenAI / Claude / Gemini / DeepSeek / Grok
                </p>
              </div>
              <div className='border-border/70 bg-card min-h-24 rounded-[10px] border p-4 shadow-xs'>
                <WalletCards className='text-muted-foreground bg-muted h-8 w-8 rounded-[8px] border p-1.5' />
                <p className='mt-4 text-sm leading-5'>
                  {t('Wallet')} / {t('Billing')} / {t('Usage logs')}
                </p>
              </div>
              <div className='border-border/70 bg-card min-h-24 rounded-[10px] border p-4 shadow-xs'>
                {signupBonus ? (
                  <Gift className='text-muted-foreground bg-muted h-8 w-8 rounded-[8px] border p-1.5' />
                ) : (
                  <ShieldCheck className='text-muted-foreground bg-muted h-8 w-8 rounded-[8px] border p-1.5' />
                )}
                <p className='mt-4 text-sm leading-5'>
                  {signupBonus
                    ? t('Sign up now and receive {{amount}} in bonus credit', {
                        amount: `$${signupBonus}`,
                      })
                    : `${t('API Keys')} / ${t('Playground')}`}
                </p>
              </div>
            </div>
          </aside>

          <section className='mx-auto w-full max-w-[460px]'>
            <div className='mb-6 lg:hidden'>
              <Link
                to='/'
                className='inline-flex items-center gap-2.5 transition-opacity hover:opacity-80'
              >
                <div className='relative h-9 w-9 shrink-0'>
                  {loading ? (
                    <Skeleton className='absolute inset-0 rounded-xl' />
                  ) : (
                    <img
                      src={logo}
                      alt={t('Logo')}
                      className='border-border/70 bg-background h-9 w-9 rounded-[8px] border object-cover shadow-sm'
                    />
                  )}
                </div>
                {loading ? (
                  <Skeleton className='h-6 w-28' />
                ) : (
                  <span className='text-xl font-semibold'>{systemName}</span>
                )}
              </Link>
              <p className='text-muted-foreground mt-6 text-sm font-medium'>
                {modeLabel}
              </p>
              <h1 className='mt-2 text-3xl font-semibold tracking-tight'>
                {modeTitle}
              </h1>
              <p className='text-muted-foreground mt-2 text-sm'>
                {t('Multi-model AI API')}
              </p>
              {signupBonus ? (
                <div className='border-primary/20 bg-primary/5 mt-4 inline-flex items-center gap-2 rounded-full border px-3 py-1.5 text-xs font-medium'>
                  <Gift className='h-3.5 w-3.5' />
                  {t('Sign up now and receive {{amount}} in bonus credit', {
                    amount: `$${signupBonus}`,
                  })}
                </div>
              ) : null}
            </div>

            <div
              data-testid='auth-card'
              className='border-border/80 bg-card rounded-[10px] border p-5 shadow-[0_1px_2px_rgba(24,24,27,0.06),0_1px_0_rgba(24,24,27,0.03)] sm:p-6'
            >
              <nav
                data-testid='auth-mode-tabs'
                aria-label={`${t('Sign in')} / ${t('Sign up')}`}
                className='bg-muted grid grid-cols-2 rounded-[8px] p-1'
              >
                <Link
                  to='/sign-in'
                  aria-current={mode === 'sign-in' ? 'page' : undefined}
                  className={cn(
                    'flex h-9 items-center justify-center rounded-[6px] text-sm font-medium transition-colors',
                    mode === 'sign-in'
                      ? 'bg-background text-foreground shadow-sm'
                      : 'text-muted-foreground hover:text-foreground'
                  )}
                >
                  {t('Sign in')}
                </Link>
                {showSignUp ? (
                  <Link
                    to='/sign-up'
                    aria-current={mode === 'sign-up' ? 'page' : undefined}
                    className={cn(
                      'flex h-9 items-center justify-center rounded-[6px] text-sm font-medium transition-colors',
                      mode === 'sign-up'
                        ? 'bg-background text-foreground shadow-sm'
                        : 'text-muted-foreground hover:text-foreground'
                    )}
                  >
                    {t('Sign up')}
                  </Link>
                ) : (
                  <span className='text-muted-foreground/60 flex h-9 items-center justify-center rounded-[6px] text-sm font-medium'>
                    {t('Sign up')}
                  </span>
                )}
              </nav>

              <div className='mt-5 [&_[data-slot=button]]:min-h-11 [&_[data-slot=button]]:rounded-[8px] [&_[data-slot=input]]:h-11 [&_[data-slot=input]]:rounded-[8px] [&_[data-slot=input]]:px-4 [&_label]:text-[13px]'>
                {children}
              </div>
            </div>

            <div className='mt-5 text-center text-sm'>
              <p className='text-muted-foreground'>
                {switchPrompt}{' '}
                {mode === 'sign-in' && !showSignUp ? (
                  <span className='text-muted-foreground/60'>
                    {switchLabel}
                  </span>
                ) : (
                  <Link
                    to={switchTarget}
                    className='text-foreground font-medium underline underline-offset-4 hover:opacity-75'
                  >
                    {switchLabel}
                  </Link>
                )}
              </p>
              <Link
                to='/'
                className='text-muted-foreground hover:text-foreground mt-4 inline-flex items-center gap-1.5 text-xs transition-colors'
              >
                <ArrowLeft className='h-3.5 w-3.5' />
                {t('Back to Home')}
              </Link>
            </div>
          </section>
        </div>
      </div>
    </main>
  )
}
