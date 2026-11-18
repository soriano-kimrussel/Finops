export type Role = 'owner' | 'admin' | 'accountant' | 'project_manager' | 'viewer'

export type User = { id: string; email: string; name: string }

export type Organization = { id: string; name: string; base_currency: string; timezone: string }

export type Membership = { organization: Organization; role: Role }

export type Me = { user: User; memberships: Membership[] }

export type OrgContext = { organization: Organization; role: Role; permissions: string[] }

export type Member = { id: string; user_id: string; name: string; email: string; role: Role; joined_at: string }

export type Currency = { code: string; name: string; minor_units: number }

export type Page<T> = { data: T[]; total?: number; next_cursor?: string | null }

export const roleLabels: Record<Role, string> = {
  owner: 'Owner',
  admin: 'Administrator',
  accountant: 'Accountant',
  project_manager: 'Project Manager',
  viewer: 'Viewer',
}
