import type { RouteObject } from 'react-router'
import { AuditLogsPage } from '@/pages/audit/AuditLogsPage'
import { IntegrationsPage } from '@/pages/integrations/IntegrationsPage'
import { ReconciliationPage } from '@/pages/reconciliation/ReconciliationPage'
import { BudgetsPage } from '@/pages/budgets/BudgetsPage'
import { CustomerDetailPage } from '@/pages/customers/CustomerDetailPage'
import { CustomersPage } from '@/pages/customers/CustomersPage'
import { ExpensesPage } from '@/pages/expenses/ExpensesPage'
import { InvoiceDetailPage } from '@/pages/invoices/InvoiceDetailPage'
import { InvoiceEditorPage } from '@/pages/invoices/InvoiceEditorPage'
import { InvoicesPage } from '@/pages/invoices/InvoicesPage'
import { PaymentsPage } from '@/pages/payments/PaymentsPage'
import { ProjectDetailPage } from '@/pages/projects/ProjectDetailPage'
import { ProjectsPage } from '@/pages/projects/ProjectsPage'

export const orgPageRoutes: RouteObject[] = [
  { path: 'overview', lazy: () => import('@/pages/overview/OverviewPage').then((m) => ({ Component: m.OverviewPage })) },
  { path: 'customers', element: <CustomersPage /> },
  { path: 'customers/:id', element: <CustomerDetailPage /> },
  { path: 'projects', element: <ProjectsPage /> },
  { path: 'projects/:id', element: <ProjectDetailPage /> },
  { path: 'budgets', element: <BudgetsPage /> },
  { path: 'invoices', element: <InvoicesPage /> },
  { path: 'invoices/new', element: <InvoiceEditorPage /> },
  { path: 'invoices/:id', element: <InvoiceDetailPage /> },
  { path: 'invoices/:id/edit', element: <InvoiceEditorPage /> },
  { path: 'payments', element: <PaymentsPage /> },
  { path: 'expenses', element: <ExpensesPage /> },
  { path: 'reports', lazy: () => import('@/pages/reports/ReportsPage').then((m) => ({ Component: m.ReportsPage })) },
  { path: 'integrations', element: <IntegrationsPage /> },
  { path: 'reconciliation', element: <ReconciliationPage /> },
  { path: 'audit-logs', element: <AuditLogsPage /> },
]
