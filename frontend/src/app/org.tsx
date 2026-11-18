import { useQuery } from '@tanstack/react-query'
import { createContext, useContext, type ReactNode } from 'react'
import { api } from '@/lib/api'
import type { OrgContext, Role } from '@/lib/types'

export type OrgValue = {
  orgId: string
  organization: OrgContext['organization']
  role: Role
  can: (permission: string) => boolean
  path: (sub: string) => string
}

const Ctx = createContext<OrgValue | null>(null)

export function useOrgQuery(orgId: string) {
  return useQuery({
    queryKey: ['org', orgId],
    queryFn: () => api<OrgContext>(`/orgs/${orgId}`),
    staleTime: 60_000,
    retry: false,
  })
}

export function OrgProvider({ value, children }: { value: OrgContext; children: ReactNode }) {
  const perms = new Set(value.permissions)
  const orgId = value.organization.id
  const v: OrgValue = {
    orgId,
    organization: value.organization,
    role: value.role,
    can: (p) => perms.has(p),
    path: (sub) => `/o/${orgId}/${sub.replace(/^\//, '')}`,
  }
  return <Ctx.Provider value={v}>{children}</Ctx.Provider>
}

export function useOrg(): OrgValue {
  const v = useContext(Ctx)
  if (!v) throw new Error('useOrg must be used inside an organization route')
  return v
}

// Keys are scoped by organization so cached data can never bleed between tenants.
export function orgKey(orgId: string, ...parts: unknown[]) {
  return ['org', orgId, ...parts] as const
}
