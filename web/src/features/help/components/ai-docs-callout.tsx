import { ExternalLink, Sparkles } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import { getDocsOrigin } from '../lib/docs-utils'

/** 「AI 可读文档」呼出框：展示 llms-full.txt 链接并提供复制/打开 */
export function AiDocsCallout({
  compact = false,
  className,
}: {
  compact?: boolean
  className?: string
}) {
  const { t } = useTranslation()
  const llmsFullUrl = `${getDocsOrigin()}/llms-full.txt`

  return (
    <section
      className={cn(
        'border-border bg-muted/40 rounded-xl border p-4 sm:p-5',
        className
      )}
    >
      <div
        className={cn(
          'flex gap-4',
          compact
            ? 'flex-col'
            : 'flex-col sm:flex-row sm:items-center sm:justify-between'
        )}
      >
        <div className='min-w-0 space-y-1.5'>
          <h2 className='text-foreground flex items-center gap-2 text-sm font-semibold'>
            <Sparkles className='text-primary size-4' />
            {t('AI-readable docs')}
          </h2>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Feed this Markdown link to any AI assistant so it can learn this site, the API and the model catalog.'
            )}
          </p>
          <code className='text-foreground/90 bg-background border-border block w-fit max-w-full truncate rounded-md border px-2 py-1 font-mono text-xs'>
            {llmsFullUrl}
          </code>
        </div>
        <div className='flex shrink-0 items-center gap-2'>
          <CopyButton
            value={llmsFullUrl}
            variant='default'
            size='sm'
            className='px-3'
          >
            {t('Copy AI link')}
          </CopyButton>
          <Button
            variant='outline'
            size='sm'
            render={
              <a href={llmsFullUrl} target='_blank' rel='noopener noreferrer' />
            }
          >
            <ExternalLink />
            {t('Open')}
          </Button>
        </div>
      </div>
    </section>
  )
}
