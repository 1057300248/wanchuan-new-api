import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { cn } from '@/lib/utils'

export function ApiCodeBlock({
  code,
  label,
  className,
}: {
  code: string
  label: string
  className?: string
}) {
  const { t } = useTranslation()

  return (
    <div
      className={cn(
        'border-border bg-muted/20 overflow-hidden rounded-xl border',
        className
      )}
    >
      <div className='border-border bg-muted/40 flex items-center justify-between border-b px-3 py-2'>
        <span className='text-muted-foreground font-mono text-[11px] font-medium'>
          {label}
        </span>
        <CopyButton
          value={code}
          size='sm'
          className='h-6 px-2 text-[11px]'
          aria-label={t('Copy code')}
        >
          {t('Copy')}
        </CopyButton>
      </div>
      <pre className='max-h-[28rem] overflow-auto p-3 text-xs leading-5'>
        <code>{code}</code>
      </pre>
    </div>
  )
}
