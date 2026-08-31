import { createFileRoute } from '@tanstack/react-router'

import { ApiReferencePage } from '@/features/help'

type ApiReferenceSearch = {
  endpoint?: string
}

export const Route = createFileRoute('/help/api/')({
  validateSearch: (search: Record<string, unknown>): ApiReferenceSearch => {
    const endpoint = search.endpoint
    return {
      endpoint: typeof endpoint === 'string' ? endpoint : undefined,
    }
  },
  component: ApiReferenceRoute,
})

function ApiReferenceRoute() {
  const { endpoint } = Route.useSearch()
  const navigate = Route.useNavigate()

  return (
    <ApiReferencePage
      endpointId={endpoint}
      onEndpointChange={(nextEndpoint) => {
        void navigate({
          search: { endpoint: nextEndpoint },
          replace: true,
        })
      }}
    />
  )
}
