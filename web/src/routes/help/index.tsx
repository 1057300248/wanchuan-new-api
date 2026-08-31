import { createFileRoute } from '@tanstack/react-router'

import { DocsIndexPage } from '@/features/help'

export const Route = createFileRoute('/help/')({
  component: DocsIndexPage,
})
