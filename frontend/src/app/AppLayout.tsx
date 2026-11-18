import * as Dropdown from '@radix-ui/react-dropdown-menu'
import clsx from 'clsx'
import {
  ArrowLeftRight,
  BarChart3,
  Building2,
  Check,
  ChevronsUpDown,
  CreditCard,
  FileText,
  FolderKanban,
  LayoutDashboard,
  LogOut,
  Menu,
  PiggyBank,
  Plug,
  Plus,
  Receipt,
  ScrollText,
  Settings,
  Users,
  X,
} from 'lucide-react'
import { useState } from 'react'
import { Link, NavLink, Navigate, Outlet, useNavigate, useParams } from 'react-router'
import { ErrorState, Spinner } from '@/components/ui'
import { ApiError } from '@/lib/api'
import { roleLabels } from '@/lib/types'
import { useLogout, useMe } from './auth'
import { OrgProvider, useOrg, useOrgQuery } from './org'

const nav = [
  { to: 'overview', label: 'Overview', icon: LayoutDashboard },
  { to: 'customers', label: 'Customers', icon: Users, perm: 'customers.view' },
  { to: 'projects', label: 'Projects', icon: FolderKanban, perm: 'projects.view' },
  { to: 'invoices', label: 'Invoices', icon: FileText, perm: 'invoices.view' },
  { to: 'payments', label: 'Payments', icon: CreditCard, perm: 'invoices.view' },
  { to: 'expenses', label: 'Expenses', icon: Receipt, perm: 'expenses.view' },
  { to: 'budgets', label: 'Budgets', icon: PiggyBank, perm: 'budgets.view' },
  { to: 'reports', label: 'Reports', icon: BarChart3, perm: 'reports.view' },
  { to: 'reconciliation', label: 'Reconciliation', icon: ArrowLeftRight, perm: 'reconciliation.view' },
  { to: 'integrations', label: 'Integrations', icon: Plug, perm: 'integrations.view' },
  { to: 'audit-logs', label: 'Audit Logs', icon: ScrollText, perm: 'audit.view' },
  { to: 'settings', label: 'Settings', icon: Settings },
]

export function OrgLayout() {
  const { orgId = '' } = useParams()
  const org = useOrgQuery(orgId)
  if (org.isPending) return <Spinner label="Loading organization" />
  if (org.error) {
    if (org.error instanceof ApiError && org.error.status === 404) return <Navigate to="/" replace />
    return <ErrorState error={org.error} onRetry={() => org.refetch()} />
  }
  return (
    <OrgProvider value={org.data}>
      <Shell />
    </OrgProvider>
  )
}

function Shell() {
  const [mobileOpen, setMobileOpen] = useState(false)
  return (
    <div className="flex h-full">
      <aside className="hidden w-60 shrink-0 border-r border-slate-200 bg-white lg:flex lg:flex-col">
        <Sidebar />
      </aside>
      {mobileOpen && (
        <div className="fixed inset-0 z-40 lg:hidden">
          <div className="absolute inset-0 bg-slate-900/40" onClick={() => setMobileOpen(false)} />
          <aside className="relative flex h-full w-64 flex-col bg-white shadow-xl">
            <button className="absolute right-3 top-3 rounded p-1 text-slate-500" aria-label="Close navigation" onClick={() => setMobileOpen(false)}>
              <X className="size-5" />
            </button>
            <Sidebar onNavigate={() => setMobileOpen(false)} />
          </aside>
        </div>
      )}
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar onMenu={() => setMobileOpen(true)} />
        <main className="flex-1 overflow-y-auto">
          <div className="mx-auto max-w-7xl px-4 py-6 sm:px-6 lg:px-8">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  )
}

function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const { can, path } = useOrg()
  return (
    <>
      <div className="flex h-14 items-center gap-2 border-b border-slate-100 px-5">
        <img src="/favicon.svg" alt="" className="size-7" />
        <span className="text-base font-semibold tracking-tight text-slate-900">FinOps HQ</span>
      </div>
      <nav aria-label="Main" className="flex-1 space-y-0.5 overflow-y-auto px-3 py-4">
        {nav
          .filter((item) => !item.perm || can(item.perm))
          .map((item) => (
            <NavLink
              key={item.to}
              to={path(item.to)}
              onClick={onNavigate}
              className={({ isActive }) =>
                clsx(
                  'flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors',
                  isActive ? 'bg-brand-50 text-brand-700' : 'text-slate-600 hover:bg-slate-50 hover:text-slate-900',
                )
              }
            >
              <item.icon className="size-4 shrink-0" aria-hidden />
              {item.label}
            </NavLink>
          ))}
      </nav>
    </>
  )
}

function TopBar({ onMenu }: { onMenu: () => void }) {
  const { organization, role } = useOrg()
  const me = useMe()
  const logout = useLogout()
  const navigate = useNavigate()
  const memberships = me.data?.memberships ?? []

  return (
    <header className="flex h-14 shrink-0 items-center gap-3 border-b border-slate-200 bg-white px-4 sm:px-6">
      <button className="rounded p-1.5 text-slate-500 hover:bg-slate-100 lg:hidden" aria-label="Open navigation" onClick={onMenu}>
        <Menu className="size-5" />
      </button>

      <Dropdown.Root>
        <Dropdown.Trigger asChild>
          <button className="flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-slate-50" aria-label="Switch organization">
            <span className="flex size-7 items-center justify-center rounded bg-slate-100 text-slate-500">
              <Building2 className="size-4" aria-hidden />
            </span>
            <span className="min-w-0">
              <span className="block truncate text-sm font-semibold text-slate-900">{organization.name}</span>
              <span className="block text-xs text-slate-500">
                {roleLabels[role]} · {organization.base_currency}
              </span>
            </span>
            <ChevronsUpDown className="size-4 text-slate-400" aria-hidden />
          </button>
        </Dropdown.Trigger>
        <Dropdown.Portal>
          <Dropdown.Content align="start" sideOffset={6} className="z-50 min-w-64 rounded-md border border-slate-200 bg-white p-1 shadow-lg">
            <Dropdown.Label className="px-2 py-1.5 text-xs font-medium uppercase tracking-wide text-slate-400">Organizations</Dropdown.Label>
            {memberships.map((m) => (
              <Dropdown.Item
                key={m.organization.id}
                onSelect={() => navigate(`/o/${m.organization.id}/overview`)}
                className="flex cursor-pointer items-center justify-between gap-3 rounded px-2 py-1.5 text-sm text-slate-700 outline-none data-[highlighted]:bg-slate-100"
              >
                <span className="truncate">{m.organization.name}</span>
                {m.organization.id === organization.id && <Check className="size-4 text-brand-600" aria-hidden />}
              </Dropdown.Item>
            ))}
            <Dropdown.Separator className="my-1 h-px bg-slate-100" />
            <Dropdown.Item asChild className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm text-slate-700 outline-none data-[highlighted]:bg-slate-100">
              <Link to="/orgs/new">
                <Plus className="size-4" aria-hidden /> Create organization
              </Link>
            </Dropdown.Item>
          </Dropdown.Content>
        </Dropdown.Portal>
      </Dropdown.Root>

      <div className="ml-auto flex items-center gap-3">
        <div className="hidden text-right sm:block">
          <p className="text-sm font-medium text-slate-900">{me.data?.user.name}</p>
          <p className="text-xs text-slate-500">{me.data?.user.email}</p>
        </div>
        <button
          className="flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm text-slate-600 hover:bg-slate-100"
          onClick={() => logout.mutate(undefined, { onSettled: () => navigate('/login') })}
        >
          <LogOut className="size-4" aria-hidden />
          <span className="hidden sm:inline">Log out</span>
        </button>
      </div>
    </header>
  )
}
