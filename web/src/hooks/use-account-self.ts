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
import { useEffect } from 'react'

import { getSelf } from '@/lib/api'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

/** Shared, identity-scoped balance snapshot for the overview and wallet. */
export function useAccountSelf() {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const query = useQuery({
    queryKey: ['account', userId, 'self'],
    enabled: Boolean(userId),
    queryFn: async () => {
      const response = await getSelf()
      if (!response.success || !response.data || response.data.id !== userId) {
        throw new Error('Account request failed')
      }
      return response.data as AuthUser
    },
    staleTime: 30_000,
  })
  useEffect(() => {
    const auth = useAuthStore.getState().auth
    if (
      !query.isFetchedAfterMount ||
      !query.isSuccess ||
      !query.data ||
      auth.user?.id !== userId ||
      !auth.user
    ) {
      return
    }
    auth.setUser({
      ...auth.user,
      quota: query.data.quota ?? auth.user.quota,
      used_quota: query.data.used_quota ?? auth.user.used_quota,
      request_count: query.data.request_count ?? auth.user.request_count,
    })
  }, [
    query.data,
    query.dataUpdatedAt,
    query.isFetchedAfterMount,
    query.isSuccess,
    userId,
  ])
  return query
}
