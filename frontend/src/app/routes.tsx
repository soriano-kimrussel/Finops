import type { RouteObject } from 'react-router'
import { OverviewPage } from '@/pages/overview/OverviewPage'

export const orgPageRoutes: RouteObject[] = [{ path: 'overview', element: <OverviewPage /> }]
