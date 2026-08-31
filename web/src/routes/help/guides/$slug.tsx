import { createFileRoute } from '@tanstack/react-router'

import { GuidePage } from '@/features/help'

export const Route = createFileRoute('/help/guides/$slug')({
  component: GuideRoute,
})

function GuideRoute() {
  const { slug } = Route.useParams()
  return <GuidePage slug={slug} />
}
