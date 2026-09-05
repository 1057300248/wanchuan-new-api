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
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import {
  ArrowRight,
  BookOpen,
  Check,
  ChevronDown,
  ChevronUp,
  Circle,
  Copy,
  CreditCard,
  FileText,
  KeyRound,
  ListChecks,
  RadioTower,
  ShieldCheck,
  TerminalSquare,
  Timer,
  type LucideIcon,
} from 'lucide-react'
import { motion, useReducedMotion } from 'motion/react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  CardStaggerContainer,
  CardStaggerItem,
} from '@/components/page-transition'
import { Button } from '@/components/ui/button'
import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useThemeCustomization } from '@/context/theme-customization-provider'
import { fetchTokenKey, getApiKeys } from '@/features/keys/api'
import type { ApiKey } from '@/features/keys/types'
import { useAccountSelf } from '@/hooks/use-account-self'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { getUserModels } from '@/lib/api'
import { MOTION_TRANSITION } from '@/lib/motion'
import { MARKETING_HOME_URL, QUICKSTART_DOCS_URL } from '@/lib/public-site'
import { ROLE } from '@/lib/roles'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  useApiInfo,
  useDashboardContentVisibility,
} from '../../hooks/use-status-data'
import { AnnouncementsPanel } from './announcements-panel'
import { ApiInfoPanel } from './api-info-panel'
import { FAQPanel } from './faq-panel'
import { PerformanceHealthPanel } from './performance-health-panel'
import { SummaryCards } from './summary-cards'
import { UptimePanel } from './uptime-panel'
import { WorkspaceActivity } from './workspace-activity'

const SETUP_GUIDE_VISIBILITY_STORAGE_KEY =
  'dashboard_overview_setup_guide_expanded'

type InternalDashboardActionPath =
  | '/keys'
  | '/wallet'
  | '/channels'
  | '/usage-logs'
type ExternalDashboardActionPath = '/playground' | '/model-pricing/'

type DashboardActionTarget =
  | { to: InternalDashboardActionPath; external?: false }
  | { to: ExternalDashboardActionPath; external: true }

type StartStep = DashboardActionTarget & {
  title: string
  description: string
  icon: LucideIcon
  completed: boolean
}

type QuickAction = DashboardActionTarget & {
  title: string
  description: string
  icon: LucideIcon
  adminOnly?: boolean
}

interface RequestExample {
  baseUrl: string
  endpoint: string
  model: string
  keyName: string
  keyId?: number
  displayKey: string
  ready: boolean
  loading: boolean
  unavailable: boolean
}

interface HeroSignal {
  label: string
  value: string
  icon: LucideIcon
  tone: IconBadgeTone
}

function getSavedSetupGuideExpanded(): boolean | null {
  if (typeof window === 'undefined') return null
  const saved = window.localStorage.getItem(SETUP_GUIDE_VISIBILITY_STORAGE_KEY)
  if (saved === 'expanded') return true
  if (saved === 'collapsed') return false
  return null
}

function saveSetupGuideExpanded(expanded: boolean): void {
  if (typeof window === 'undefined') return
  window.localStorage.setItem(
    SETUP_GUIDE_VISIBILITY_STORAGE_KEY,
    expanded ? 'expanded' : 'collapsed'
  )
}

function getCurrentOrigin(): string {
  if (typeof window === 'undefined') return ''
  return window.location.origin
}

function normalizeEndpoint(sourceUrl?: string): string {
  const fallback = `${getCurrentOrigin()}/v1/chat/completions`
  const trimmed = sourceUrl?.trim()
  if (!trimmed) return fallback

  const withoutTrailingSlash = trimmed.replace(/\/+$/, '')
  if (withoutTrailingSlash.endsWith('/v1/chat/completions')) {
    return withoutTrailingSlash
  }
  if (withoutTrailingSlash.endsWith('/v1')) {
    return `${withoutTrailingSlash}/chat/completions`
  }
  return `${withoutTrailingSlash}/v1/chat/completions`
}

function getPreferredKey(keys: ApiKey[], models: string[]): ApiKey | null {
  return (
    keys.find((key) => {
      if (key.status !== 1 || (!key.unlimited_quota && key.remain_quota <= 0)) {
        return false
      }
      if (key.expired_time !== -1 && key.expired_time <= Date.now() / 1000) {
        return false
      }
      if (!key.model_limits_enabled || models.length === 0) return true
      const allowed = new Set(
        (key.model_limits ?? '').split(',').map((model) => model.trim())
      )
      return models.some((model) => allowed.has(model))
    }) ?? null
  )
}

function quoteShellArgument(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`
}

function buildCurlCommand(args: {
  endpoint: string
  apiKey: string
  model: string
}): string {
  return [
    `curl ${quoteShellArgument(args.endpoint)} \\`,
    '  -H "Content-Type: application/json" \\',
    `  -H ${quoteShellArgument(`Authorization: Bearer ${args.apiKey}`)} \\`,
    `  -d ${quoteShellArgument(JSON.stringify({ model: args.model, messages: [{ role: 'user', content: 'Say hello in one sentence.' }] }))}`,
  ].join('\n')
}

function StartStepItem(props: {
  step: StartStep
  index: number
  isLast: boolean
}) {
  const StatusIcon = props.step.completed ? Check : Circle

  return (
    <li className='relative flex gap-3 pb-2.5 last:pb-0'>
      {!props.isLast && (
        <span
          className='bg-border absolute top-9 bottom-0 left-4 w-px'
          aria-hidden='true'
        />
      )}
      <span
        className={cn(
          'bg-background relative z-10 flex size-8 shrink-0 items-center justify-center rounded-lg border ',
          props.step.completed && 'border-success/30 bg-success/10'
        )}
      >
        <StatusIcon
          className={props.step.completed ? 'text-success size-4' : 'size-4'}
          aria-hidden='true'
        />
      </span>

      {props.step.external ? (
        <a
          href={`${MARKETING_HOME_URL}${props.step.to}`}
          className='bg-background/70 hover:bg-muted/50 focus-visible:ring-ring flex min-w-0 flex-1 items-center justify-between gap-3 rounded-lg border px-3 py-2.5 text-left transition-colors outline-none focus-visible:ring-2'
        >
          <StartStepContent step={props.step} index={props.index} />
        </a>
      ) : (
        <Link
          to={props.step.to}
          className='bg-background/70 hover:bg-muted/50 focus-visible:ring-ring flex min-w-0 flex-1 items-center justify-between gap-3 rounded-lg border px-3 py-2.5 text-left transition-colors outline-none focus-visible:ring-2'
        >
          <StartStepContent step={props.step} index={props.index} />
        </Link>
      )}
    </li>
  )
}

function StartStepContent(props: { step: StartStep; index: number }) {
  const Icon = props.step.icon
  return (
    <>
      <span className='flex min-w-0 items-start gap-2.5'>
        <span className='bg-muted mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-lg'>
          <Icon className='size-3.5' aria-hidden='true' />
        </span>
        <span className='flex min-w-0 flex-col gap-0.5'>
          <span className='flex items-center gap-2 text-sm font-medium'>
            <span className='text-muted-foreground font-mono text-xs tabular-nums'>
              {props.index + 1}.
            </span>
            <span className='truncate'>{props.step.title}</span>
          </span>
          <span className='text-muted-foreground line-clamp-1 text-xs'>
            {props.step.description}
          </span>
        </span>
      </span>
      <ArrowRight
        className='text-muted-foreground size-4 shrink-0'
        aria-hidden='true'
      />
    </>
  )
}

function RequestPreview(props: {
  example: RequestExample
  signals: HeroSignal[]
}) {
  const { t } = useTranslation()
  const reducedMotion = useReducedMotion()
  const { customization } = useThemeCustomization()
  const shouldReduceMotion =
    reducedMotion || customization.preset === 'wanchuan'
  const [isCopying, setIsCopying] = useState(false)
  const { copyToClipboard } = useCopyToClipboard({ notify: false })
  const previewCurl = buildCurlCommand({
    endpoint: props.example.endpoint,
    apiKey: props.example.displayKey,
    model: props.example.model,
  })
  const previewLines = previewCurl.split('\n')
  const handleCopyRequest = async () => {
    if (!props.example.ready || !props.example.keyId || isCopying) return

    setIsCopying(true)
    try {
      const result = await fetchTokenKey(props.example.keyId)
      const key = result.success && result.data?.key ? result.data.key : ''
      if (!key) {
        toast.error(result.message || t('Failed to copy to clipboard'))
        return
      }

      const realCurl = buildCurlCommand({
        endpoint: props.example.endpoint,
        apiKey: `sk-${key}`,
        model: props.example.model,
      })
      const copied = await copyToClipboard(realCurl)
      if (copied) {
        toast.success(t('Copied to clipboard'))
      } else {
        toast.error(t('Failed to copy to clipboard'))
      }
    } catch {
      toast.error(t('Failed to copy to clipboard'))
    } finally {
      setIsCopying(false)
    }
  }

  return (
    <motion.div
      initial={shouldReduceMotion ? false : { opacity: 0, y: 10, scale: 0.98 }}
      animate={shouldReduceMotion ? undefined : { opacity: 1, y: 0, scale: 1 }}
      transition={MOTION_TRANSITION.slow}
      className='bg-card relative h-full min-w-0 overflow-hidden rounded-lg border p-4 sm:p-6'
    >
      {!shouldReduceMotion && (
        <motion.div
          className='via-foreground/30 pointer-events-none absolute inset-x-0 top-0 h-px bg-linear-to-r from-transparent to-transparent'
          animate={{ x: ['-100%', '100%'] }}
          transition={{ duration: 3.2, repeat: Infinity, ease: 'easeInOut' }}
          aria-hidden='true'
        />
      )}

      <div className='flex items-center justify-between gap-3 border-b pb-3'>
        <div className='flex min-w-0 items-center gap-2'>
          <IconBadge
            tone={customization.preset === 'wanchuan' ? 'neutral' : 'info'}
          >
            <TerminalSquare />
          </IconBadge>
          <div className='min-w-0'>
            <div className='truncate text-sm font-medium'>
              {t('First API request')}
            </div>
            <div className='text-muted-foreground text-xs break-words'>
              {props.example.keyName}
            </div>
          </div>
        </div>
        {props.example.ready && (
          <Button
            variant='outline'
            size='sm'
            className='h-7 gap-1.5 px-2 text-xs'
            disabled={isCopying}
            onClick={handleCopyRequest}
            aria-label={t('Copy ready-to-run curl')}
          >
            <Copy data-icon='inline-start' />
            {isCopying ? t('Loading') : t('Copy')}
          </Button>
        )}
        {!props.example.ready &&
          !props.example.keyId &&
          !props.example.loading &&
          !props.example.unavailable && (
            <Button size='sm' variant='outline' render={<Link to='/keys' />}>
              {t('Create API Key')}
            </Button>
          )}
      </div>

      <div className='my-4 flex min-w-0 items-center justify-between gap-3'>
        <div className='min-w-0'>
          <p className='text-muted-foreground mb-1 text-xs'>{t('Base URL')}</p>
          <code className='text-xs break-all'>{props.example.baseUrl}</code>
        </div>
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='icon-sm'
                aria-label={t('Copy Base URL')}
                onClick={async () => {
                  const copied = await copyToClipboard(props.example.baseUrl)
                  if (copied) toast.success(t('Copied to clipboard'))
                  else toast.error(t('Failed to copy to clipboard'))
                }}
              />
            }
          >
            <Copy />
          </TooltipTrigger>
          <TooltipContent>{t('Copy Base URL')}</TooltipContent>
        </Tooltip>
      </div>

      {props.example.loading && (
        <p role='status' className='py-4 text-sm'>
          {t('Loading')}
        </p>
      )}
      {!props.example.loading && props.example.unavailable && (
        <p className='text-muted-foreground py-4 text-sm'>
          {t('Failed to load data')}
        </p>
      )}
      {!props.example.loading &&
        !props.example.unavailable &&
        !props.example.model && (
          <p className='text-muted-foreground py-4 text-sm'>
            {t('No models available')}
          </p>
        )}
      {!!props.example.model && !props.example.unavailable && (
        <pre
          tabIndex={0}
          className='bg-muted/50 mb-4 max-h-48 overflow-auto rounded-md p-3 font-mono text-xs leading-relaxed'
          aria-label={t('First API request')}
        >
          <code>{previewLines.join('\n')}</code>
        </pre>
      )}

      <div className='grid gap-2'>
        {props.signals.map((signal) => {
          const Icon = signal.icon

          return (
            <div
              key={signal.label}
              className='flex min-w-0 items-start justify-between gap-3 border-t py-2'
            >
              <span className='flex min-w-0 items-center gap-2'>
                <IconBadge
                  tone={
                    customization.preset === 'wanchuan'
                      ? 'neutral'
                      : signal.tone
                  }
                  size='xs'
                >
                  <Icon />
                </IconBadge>
                <span className='truncate text-xs font-medium'>
                  {signal.label}
                </span>
              </span>
              <span className='text-muted-foreground max-w-[55%] min-w-0 text-right text-xs break-all'>
                {signal.value}
              </span>
            </div>
          )
        })}
      </div>
      <a
        href={QUICKSTART_DOCS_URL}
        className='mt-4 inline-flex items-center gap-2 text-sm underline underline-offset-4'
      >
        <BookOpen className='size-4' />
        {t('Documentation')}
      </a>
    </motion.div>
  )
}

function QuickActionItem(props: { action: QuickAction }) {
  const Icon = props.action.icon

  return (
    <Button
      variant='outline'
      className='h-auto justify-start rounded-lg px-3 py-3 text-left'
      render={
        props.action.external ? (
          <a href={`${MARKETING_HOME_URL}${props.action.to}`} />
        ) : (
          <Link to={props.action.to} />
        )
      }
    >
      <span className='bg-muted flex size-9 shrink-0 items-center justify-center rounded-lg'>
        <Icon className='size-4' aria-hidden='true' />
      </span>
      <span className='flex min-w-0 flex-1 flex-col gap-0.5'>
        <span className='truncate text-sm font-medium'>
          {props.action.title}
        </span>
        <span className='text-muted-foreground line-clamp-2 text-xs leading-relaxed'>
          {props.action.description}
        </span>
      </span>
    </Button>
  )
}

function CompactQuickAction(props: { action: QuickAction }) {
  const Icon = props.action.icon

  return (
    <Button
      variant='outline'
      size='sm'
      className='bg-background/70 h-8 min-w-24 gap-1.5 px-2.5'
      render={
        props.action.external ? (
          <a href={`${MARKETING_HOME_URL}${props.action.to}`} />
        ) : (
          <Link to={props.action.to} />
        )
      }
    >
      <Icon data-icon='inline-start' />
      <span>{props.action.title}</span>
    </Button>
  )
}

export function OverviewDashboard() {
  const account = useAccountSelf()
  const { customization } = useThemeCustomization()
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const { items: apiInfoItems } = useApiInfo()
  const {
    apiInfo: showApiInfoPanel,
    announcements: showAnnouncementsPanel,
    faq: showFAQPanel,
    uptimeKuma: showUptimePanel,
  } = useDashboardContentVisibility()
  const [manualSetupGuideExpanded, setManualSetupGuideExpanded] = useState<
    boolean | null
  >(() => getSavedSetupGuideExpanded())

  const requestCount = Number(user?.request_count ?? 0)
  const remainQuota = Number(user?.quota ?? 0)
  const usedQuota = Number(user?.used_quota ?? 0)
  const isAdmin = Boolean(user?.role && user.role >= ROLE.ADMIN)

  const apiKeysQuery = useQuery({
    queryKey: ['dashboard', user?.id, 'overview', 'api-keys'],
    enabled: Boolean(user?.id),
    queryFn: async () => {
      const result = await getApiKeys({ p: 1, size: 10 })
      if (!result.success) throw new Error('API keys request failed')
      return result.data?.items ?? []
    },
    staleTime: 60 * 1000,
  })

  const modelsQuery = useQuery({
    queryKey: ['dashboard', user?.id, 'overview', 'user-models'],
    enabled: Boolean(user?.id),
    queryFn: async () => {
      const result = await getUserModels()
      if (!result.success) throw new Error('Models request failed')
      return result.data ?? []
    },
    staleTime: 5 * 60 * 1000,
  })

  const preferredKey = useMemo(
    () => getPreferredKey(apiKeysQuery.data ?? [], modelsQuery.data ?? []),
    [apiKeysQuery.data, modelsQuery.data]
  )

  const startSteps = useMemo<StartStep[]>(
    () => [
      {
        title: t('Create API Key'),
        description: t('Create a key for your app or service'),
        to: '/keys',
        icon: KeyRound,
        completed: Boolean(preferredKey),
      },
      {
        title: t('Add credits'),
        description: t('Keep enough balance before production traffic'),
        to: '/wallet',
        icon: CreditCard,
        completed: remainQuota > 0 || usedQuota > 0,
      },
      {
        title: t('Send a request'),
        description: t('Verify routing with Text Generation or your client'),
        to: '/playground',
        external: true,
        icon: TerminalSquare,
        completed: requestCount > 0,
      },
    ],
    [preferredKey, remainQuota, requestCount, t, usedQuota]
  )

  const quickActions = useMemo<QuickAction[]>(
    () => [
      {
        title: t('API Keys'),
        description: t('Create a key for your app or service'),
        to: '/keys',
        icon: KeyRound,
      },
      {
        title: t('Channels'),
        description: t('Configure upstream providers and routing.'),
        to: '/channels',
        icon: RadioTower,
        adminOnly: true,
      },
      {
        title: t('Usage Logs'),
        description: t('Inspect requests, errors, and billing details'),
        to: '/usage-logs',
        icon: FileText,
      },
      {
        title: t('Pricing'),
        description: t('Review model rates before scaling traffic'),
        to: '/model-pricing/',
        external: true,
        icon: BookOpen,
      },
    ],
    [t]
  )

  const visibleQuickActions = useMemo(
    () => quickActions.filter((action) => !action.adminOnly || isAdmin),
    [isAdmin, quickActions]
  )

  const selectedModel = useMemo(() => {
    const models = modelsQuery.data ?? []
    if (!preferredKey?.model_limits_enabled) return models[0] ?? ''
    const allowed = new Set(
      (preferredKey.model_limits ?? '').split(',').map((value) => value.trim())
    )
    return models.find((model) => allowed.has(model)) ?? ''
  }, [modelsQuery.data, preferredKey])
  let modelStatus = selectedModel || t('No models available')
  if (modelsQuery.isPending) modelStatus = t('Loading')
  if (modelsQuery.isError) modelStatus = t('Failed to load data')

  const heroSignals = useMemo<HeroSignal[]>(
    () => [
      {
        label: t('API Keys'),
        value: preferredKey?.name || t('No API key yet'),
        icon: ShieldCheck,
        tone: 'success',
      },
      {
        label: t('Model selected'),
        value: modelStatus,
        icon: Timer,
        tone: 'chart-4',
      },
    ],
    [modelStatus, preferredKey, t]
  )

  const requestExample = useMemo<RequestExample>(() => {
    const endpoint = normalizeEndpoint(apiInfoItems[0]?.url)
    const model = selectedModel
    const keyName = preferredKey?.name ?? t('No API key yet')
    const ready = Boolean(
      preferredKey?.id &&
      model &&
      apiKeysQuery.isSuccess &&
      modelsQuery.isSuccess
    )

    return {
      endpoint,
      baseUrl: endpoint.replace(/\/chat\/completions$/, ''),
      model,
      keyName,
      keyId: preferredKey?.id,
      displayKey: 'sk-...',
      ready,
      loading: apiKeysQuery.isPending || modelsQuery.isPending,
      unavailable: apiKeysQuery.isError || modelsQuery.isError,
    }
  }, [
    apiInfoItems,
    selectedModel,
    preferredKey,
    apiKeysQuery.isSuccess,
    modelsQuery.isSuccess,
    apiKeysQuery.isPending,
    modelsQuery.isPending,
    apiKeysQuery.isError,
    modelsQuery.isError,
    t,
  ])

  const completedStepCount = startSteps.filter((step) => step.completed).length
  const setupComplete = completedStepCount === startSteps.length
  const setupStatusReady = apiKeysQuery.isSuccess && account.isSuccess
  const setupGuideExpanded =
    manualSetupGuideExpanded ?? (setupStatusReady && !setupComplete)
  const showLeftContentPanels =
    isAdmin || showApiInfoPanel || showAnnouncementsPanel || showFAQPanel
  const showContentPanels = showLeftContentPanels || showUptimePanel

  const handleSetupGuideToggle = () => {
    const nextExpanded = !setupGuideExpanded
    setManualSetupGuideExpanded(nextExpanded)
    saveSetupGuideExpanded(nextExpanded)
  }

  if (customization.preset === 'wanchuan' && !isAdmin) {
    return (
      <div className='flex flex-col gap-6'>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <p className='text-muted-foreground min-w-0 text-sm break-all'>
            {user?.display_name || user?.username}
          </p>
          <Button size='sm' render={<Link to='/keys' />}>
            <KeyRound data-icon='inline-start' />
            {t('Create API Key')}
          </Button>
        </div>
        {(account.isError || apiKeysQuery.isError || modelsQuery.isError) && (
          <div
            role='alert'
            className='bg-card flex flex-wrap items-center gap-3 rounded-lg border p-4'
          >
            <span>{t('Failed to load data')}</span>
            <Button
              variant='outline'
              onClick={() => {
                void account.refetch()
                void apiKeysQuery.refetch()
                void modelsQuery.refetch()
              }}
            >
              {t('Retry')}
            </Button>
          </div>
        )}
        <SummaryCards />
        <section className='border-y py-4' aria-label={t('Setup guide')}>
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <h3 className='text-sm font-semibold'>{t('Setup guide')}</h3>
            <Button
              variant='ghost'
              size='sm'
              aria-expanded={setupGuideExpanded}
              aria-controls='workspace-setup-steps'
              onClick={handleSetupGuideToggle}
            >
              {setupGuideExpanded
                ? t('Hide setup guide')
                : t('Show setup guide')}
            </Button>
          </div>
          <ol
            id='workspace-setup-steps'
            hidden={!setupGuideExpanded}
            className='hidden:hidden mt-4 grid gap-3 lg:grid-cols-3'
          >
            {startSteps.map((step, index) => (
              <StartStepItem
                key={step.title}
                step={step}
                index={index}
                isLast
              />
            ))}
          </ol>
        </section>
        <WorkspaceActivity
          quickstart={
            <RequestPreview example={requestExample} signals={heroSignals} />
          }
        />
        {showApiInfoPanel && <ApiInfoPanel />}
        {showAnnouncementsPanel && <AnnouncementsPanel />}
        {showFAQPanel && <FAQPanel />}
        {showUptimePanel && <UptimePanel />}
      </div>
    )
  }

  return (
    <div className='flex flex-col gap-6'>
      {(account.isError || apiKeysQuery.isError || modelsQuery.isError) && (
        <div
          role='alert'
          className='bg-card flex flex-wrap items-center gap-3 rounded-lg border p-4'
        >
          <span>{t('Failed to load data')}</span>
          <Button
            variant='outline'
            onClick={() => {
              void account.refetch()
              void apiKeysQuery.refetch()
              void modelsQuery.refetch()
            }}
          >
            {t('Retry')}
          </Button>
        </div>
      )}
      {setupGuideExpanded ? (
        <CardStaggerContainer className='grid items-stretch gap-4 xl:grid-cols-[minmax(0,1fr)_22rem]'>
          <CardStaggerItem className='bg-card h-full overflow-hidden rounded-lg border'>
            <div className='relative h-full overflow-hidden p-4 sm:p-5'>
              <div className='relative grid gap-5 lg:grid-cols-[minmax(0,1fr)_21rem]'>
                <div className='flex min-w-0 flex-col gap-5'>
                  <div className='flex flex-wrap items-start justify-between gap-3'>
                    <div className='flex max-w-2xl flex-col gap-1'>
                      <div className='text-muted-foreground flex items-center gap-2 text-xs font-medium tracking-wider uppercase'>
                        <ListChecks className='size-3.5' aria-hidden='true' />
                        {t('Get started')}
                      </div>
                      <h3 className='text-xl font-semibold tracking-tight sm:text-2xl'>
                        {t('Build on your API gateway in minutes')}
                      </h3>
                      <p className='text-muted-foreground max-w-xl text-sm leading-relaxed'>
                        {t(
                          'A focused home for keys, balance, routing, and service health.'
                        )}
                      </p>
                    </div>
                    <div className='flex flex-wrap items-center gap-2'>
                      <Button
                        variant='outline'
                        size='sm'
                        aria-expanded={setupGuideExpanded}
                        onClick={handleSetupGuideToggle}
                      >
                        <ChevronUp data-icon='inline-start' />
                        {t('Hide setup guide')}
                      </Button>
                      <Button size='sm' render={<Link to='/keys' />}>
                        <KeyRound data-icon='inline-start' />
                        {t('Create API Key')}
                      </Button>
                    </div>
                  </div>

                  <ol className='bg-background/45 rounded-lg border p-2'>
                    {startSteps.map((step, index) => (
                      <StartStepItem
                        key={step.title}
                        step={step}
                        index={index}
                        isLast={index === startSteps.length - 1}
                      />
                    ))}
                  </ol>
                </div>

                <RequestPreview
                  example={requestExample}
                  signals={heroSignals}
                />
              </div>
            </div>
          </CardStaggerItem>

          <CardStaggerItem className='bg-card h-full rounded-lg border p-4 sm:p-5'>
            <div className='flex h-full flex-col gap-4'>
              <div className='flex flex-col gap-1'>
                <div className='text-muted-foreground text-xs font-medium tracking-wider uppercase'>
                  {t('Recommended actions')}
                </div>
                <h3 className='text-lg font-semibold tracking-tight'>
                  {t('Keep the platform ready')}
                </h3>
              </div>
              <div className='grid gap-2'>
                {visibleQuickActions.map((action) => (
                  <QuickActionItem key={action.title} action={action} />
                ))}
              </div>
            </div>
          </CardStaggerItem>
        </CardStaggerContainer>
      ) : (
        <CardStaggerContainer>
          <CardStaggerItem className='bg-card overflow-hidden rounded-lg border'>
            <div className='relative overflow-hidden px-4 py-3 sm:px-5'>
              <div className='relative flex flex-wrap items-center justify-between gap-3'>
                <div className='flex min-w-0 items-center gap-3'>
                  <span className='bg-background/70 flex size-9 shrink-0 items-center justify-center rounded-lg border'>
                    <Check className='text-success size-4' aria-hidden='true' />
                  </span>
                  <div className='min-w-0'>
                    <div className='flex items-center gap-2'>
                      <h3 className='truncate text-sm font-semibold'>
                        {setupComplete
                          ? t('Setup guide complete')
                          : t('Setup guide')}
                      </h3>
                      <span className='text-muted-foreground bg-background/60 rounded-md border px-2 py-0.5 text-xs'>
                        {t('Setup progress: {{completed}}/{{total}}', {
                          completed: completedStepCount,
                          total: startSteps.length,
                        })}
                      </span>
                    </div>
                    <p className='text-muted-foreground line-clamp-1 text-xs'>
                      {setupComplete
                        ? t(
                            'Your setup guide is collapsed so usage stays in focus.'
                          )
                        : t('Setup guide is collapsed. Expand it anytime.')}
                    </p>
                  </div>
                </div>

                <div className='flex flex-wrap items-center gap-2'>
                  {visibleQuickActions.map((action) => (
                    <CompactQuickAction key={action.title} action={action} />
                  ))}
                  <Button
                    variant='outline'
                    size='sm'
                    className='bg-background/70 h-8 min-w-28'
                    aria-expanded={setupGuideExpanded}
                    onClick={handleSetupGuideToggle}
                  >
                    <ChevronDown data-icon='inline-start' />
                    {t('Show setup guide')}
                  </Button>
                </div>
              </div>
            </div>
          </CardStaggerItem>
        </CardStaggerContainer>
      )}

      <SummaryCards />

      {showContentPanels && (
        <CardStaggerContainer
          className={cn(
            'grid grid-cols-1 gap-4',
            showLeftContentPanels &&
              showUptimePanel &&
              'xl:grid-cols-[minmax(0,1fr)_22rem]'
          )}
        >
          {showLeftContentPanels && (
            <div
              className={cn(
                'grid min-w-0 grid-cols-1 gap-4',
                (showApiInfoPanel || showAnnouncementsPanel || showFAQPanel) &&
                  'lg:grid-cols-2'
              )}
            >
              {isAdmin && (
                <CardStaggerItem className='lg:col-span-2'>
                  <PerformanceHealthPanel />
                </CardStaggerItem>
              )}
              {showApiInfoPanel && (
                <CardStaggerItem>
                  <ApiInfoPanel />
                </CardStaggerItem>
              )}
              {showAnnouncementsPanel && (
                <CardStaggerItem>
                  <AnnouncementsPanel />
                </CardStaggerItem>
              )}
              {showFAQPanel && (
                <CardStaggerItem>
                  <FAQPanel />
                </CardStaggerItem>
              )}
            </div>
          )}
          {showUptimePanel && (
            <CardStaggerItem>
              <UptimePanel />
            </CardStaggerItem>
          )}
        </CardStaggerContainer>
      )}
    </div>
  )
}
