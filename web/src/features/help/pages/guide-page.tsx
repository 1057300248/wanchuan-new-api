import { Link } from '@tanstack/react-router'
import { ArrowLeft, FileText } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'

import { DocsShell } from '../components/docs-shell'
import { DocsMarkdown } from '../components/guide-article'
import { PrevNextNav } from '../components/prev-next-nav'
import { getGuide, getGuideGroup } from '../content/nav'
import { buildGuideMarkdown } from '../lib/docs-utils'

export function GuidePage({ slug }: { slug: string }) {
  const { t } = useTranslation()
  const guide = getGuide(slug)

  if (!guide) {
    return (
      <DocsShell>
        <div className='space-y-4 py-12 text-center'>
          <h1 className='text-foreground text-2xl font-bold'>
            {t('Guide not found')}
          </h1>
          <p className='text-muted-foreground text-sm'>
            {t('The guide you are looking for does not exist or has moved.')}
          </p>
          <Button variant='outline' render={<Link to='/help' />}>
            <ArrowLeft />
            {t('Back to docs')}
          </Button>
        </div>
      </DocsShell>
    )
  }

  const group = getGuideGroup(slug)

  return (
    <DocsShell>
      <article className='space-y-6'>
        <header className='space-y-3'>
          {group ? (
            <p className='text-muted-foreground text-xs font-medium tracking-wide uppercase'>
              {group.title}
            </p>
          ) : null}
          <h1 className='text-foreground text-3xl font-bold tracking-tight'>
            {guide.title}
          </h1>
          <p className='text-muted-foreground text-sm leading-relaxed sm:text-base'>
            {guide.description}
          </p>
          <div className='flex flex-wrap items-center gap-2'>
            <CopyButton
              value={buildGuideMarkdown(guide)}
              variant='outline'
              size='sm'
              className='px-3'
            >
              <FileText />
              {t('Copy Markdown')}
            </CopyButton>
            {guide.updated ? (
              <span className='text-muted-foreground/70 text-xs'>
                {t('Last verified')}: {guide.updated}
              </span>
            ) : null}
          </div>
        </header>

        <DocsMarkdown body={guide.body} />

        <PrevNextNav slug={slug} />
      </article>
    </DocsShell>
  )
}
