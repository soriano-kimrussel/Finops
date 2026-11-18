import { Navigate, Outlet, useLocation, type RouteObject } from 'react-router'
import { ErrorState, Spinner } from '@/components/ui'
import { LoginPage, RegisterPage } from '@/pages/auth/AuthPages'
import { CreateOrgPage } from '@/pages/orgs/CreateOrgPage'
import { SettingsPage } from '@/pages/settings/SettingsPage'
import { OrgLayout } from './AppLayout'
import { useMe } from './auth'
import { orgPageRoutes } from './routes'

function RequireAuth() {
  const me = useMe()
  const location = useLocation()
  if (me.isPending) return <Spinner />
  if (me.error) return <ErrorState error={me.error} onRetry={() => me.refetch()} />
  if (!me.data) return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
  return <Outlet />
}

function Home() {
  const me = useMe()
  const first = me.data?.memberships[0]
  return <Navigate to={first ? `/o/${first.organization.id}/overview` : '/orgs/new'} replace />
}

function NotFound() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
      <p className="text-sm font-semibold text-brand-600">404</p>
      <h1 className="text-xl font-semibold text-slate-900">Page not found</h1>
      <a href="/" className="text-sm font-medium text-brand-600 hover:text-brand-700">
        Go home
      </a>
    </div>
  )
}

export const routes: RouteObject[] = [
  { path: '/login', element: <LoginPage /> },
  { path: '/register', element: <RegisterPage /> },
  {
    element: <RequireAuth />,
    children: [
      { index: true, element: <Home /> },
      { path: '/orgs/new', element: <CreateOrgPage /> },
      {
        path: '/o/:orgId',
        element: <OrgLayout />,
        children: [{ index: true, element: <Navigate to="overview" replace /> }, ...orgPageRoutes, { path: 'settings', element: <SettingsPage /> }],
      },
    ],
  },
  { path: '*', element: <NotFound /> },
]
