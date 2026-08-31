import { HelpCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Markdown } from '@/components/ui/markdown'
import { useFAQ } from '@/features/dashboard/hooks/use-status-data'
import type { FAQItem } from '@/features/dashboard/types'

import { fallbackFaq } from '../content/faq'
import { applyBaseUrl } from '../lib/docs-utils'

/** 常见问答：优先展示管理后台配置的 FAQ，未配置时用静态兜底 */
export function FaqSection() {
  const { t } = useTranslation()
  const { items, loading } = useFAQ()
  const list: readonly FAQItem[] =
    !loading && items.length > 0 ? items : fallbackFaq

  if (loading && items.length === 0) {
    return null
  }

  return (
    <section className='space-y-3'>
      <h2 className='text-foreground flex items-center gap-2 text-lg font-semibold'>
        <HelpCircle className='text-primary size-5' />
        {t('FAQ')}
      </h2>
      <Accordion className='border-border bg-card w-full rounded-xl border px-4'>
        {list.map((item, index) => {
          const key = item.id ?? `faq-${index}`
          return (
            <AccordionItem key={key} value={`item-${key}`}>
              <AccordionTrigger className='text-start hover:no-underline'>
                <Markdown className='text-sm leading-relaxed font-medium'>
                  {applyBaseUrl(item.question)}
                </Markdown>
              </AccordionTrigger>
              <AccordionContent>
                <Markdown className='text-muted-foreground text-sm'>
                  {applyBaseUrl(item.answer)}
                </Markdown>
              </AccordionContent>
            </AccordionItem>
          )
        })}
      </Accordion>
    </section>
  )
}
