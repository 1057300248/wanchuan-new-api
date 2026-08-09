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
import { useTranslation } from 'react-i18next'

import { useStatus } from '@/hooks/use-status'

import { AuthLayout } from '../auth-layout'
import { TermsFooter } from '../components/terms-footer'
import { SignUpForm } from './components/sign-up-form'

function formatSignupBonus(status: ReturnType<typeof useStatus>['status']) {
  const enabled =
    status?.signup_bonus_enabled ?? status?.data?.signup_bonus_enabled ?? false
  const quota = status?.signup_bonus_quota ?? status?.data?.signup_bonus_quota ?? 0
  const quotaPerUnit = status?.quota_per_unit ?? status?.data?.quota_per_unit ?? 0
  if (!enabled || quota <= 0 || quotaPerUnit <= 0) return null
  const dollars = quota / quotaPerUnit
  return dollars.toLocaleString(undefined, { maximumFractionDigits: 2 })
}

export function SignUp() {
  const { t } = useTranslation()
  const { status } = useStatus()
  const signupBonus = formatSignupBonus(status)

  return (
    <AuthLayout>
      <div className='w-full space-y-8'>
        <div className='space-y-2'>
          <h2 className='text-center text-2xl font-semibold tracking-tight sm:text-left'>
            {t('Create an account')}
          </h2>
          <p className='text-muted-foreground text-left text-sm sm:text-base'>
            {t('Already have an account?')}{' '}
            <Link
              to='/sign-in'
              className='hover:text-primary font-medium underline underline-offset-4'
            >
              {t('Sign in')}
            </Link>
            .
          </p>
          {signupBonus ? (
            <div className='border-primary/25 bg-primary/5 text-foreground rounded-lg border px-3 py-2 text-sm'>
              {t('Sign up now and receive {{amount}} in bonus credit', {
                amount: `$${signupBonus}`,
              })}
              <span className='text-muted-foreground ml-1'>
                {t(
                  'Bonus credit is available only for eligible models; validity and restrictions are subject to platform rules.'
                )}
              </span>
            </div>
          ) : null}
        </div>

        <SignUpForm />

        <TermsFooter
          variant='sign-up'
          status={status}
          className='text-center'
        />
      </div>
    </AuthLayout>
  )
}
