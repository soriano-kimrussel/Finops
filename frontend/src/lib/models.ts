export type ListResponse<T> = { data: T[]; total: number }

export type Customer = {
  id: string
  name: string
  contact_name: string | null
  email: string | null
  phone: string | null
  address_line1: string | null
  address_line2: string | null
  city: string | null
  region: string | null
  postal_code: string | null
  country: string | null
  currency: string
  status: 'active' | 'inactive'
  notes: string | null
  source: 'internal' | 'quickbooks'
  external_id: string | null
  created_at: string
  updated_at: string
  outstanding?: string
  invoice_count?: number
}

export type CustomerDetail = {
  customer: Customer
  summary: { currency: string; invoiced: string; paid: string; outstanding: string; overdue: string }[]
  invoices: InvoiceListItem[]
  projects: { id: string; name: string; code: string | null; status: ProjectStatus; start_date: string; end_date: string | null }[]
}

export type BudgetLevel = 'none' | 'ok' | 'warning' | 'over'

export type BudgetSummary = {
  total: string
  actual: string
  pending: string
  remaining: string
  percent_used: string
  level: BudgetLevel
}

export type ProjectStatus = 'planned' | 'active' | 'on_hold' | 'completed' | 'cancelled'

export type Project = {
  id: string
  customer_id: string
  customer_name: string
  name: string
  code: string | null
  description: string | null
  status: ProjectStatus
  currency: string
  start_date: string
  end_date: string | null
  created_at: string
  budget: BudgetSummary
}

export type BudgetLine = {
  id: string
  project_id: string
  project_name?: string
  category_id: string | null
  category_name: string
  amount: string
  currency: string
  notes: string | null
  version: number
  consumption: BudgetSummary
}

export type ProjectDetail = {
  project: Project
  budget_lines: BudgetLine[]
  unbudgeted: { approved: string; pending: string }
  billing: { billed: string; collected: string; outstanding: string; invoice_count: number }
}

export type BudgetOverview = {
  project_id: string
  project_name: string
  project_status: ProjectStatus
  currency: string
  lines: BudgetLine[]
  totals: BudgetSummary
}

export type InvoiceStatus = 'draft' | 'sent' | 'partially_paid' | 'paid' | 'void'
export type DisplayStatus = InvoiceStatus | 'overdue'

export type InvoiceListItem = {
  id: string
  number: string
  status: InvoiceStatus
  display_status?: DisplayStatus
  is_overdue: boolean
  customer_id?: string
  customer_name?: string
  project_id?: string | null
  project_name?: string | null
  currency: string
  issue_date: string
  due_date: string
  total: string
  amount_paid: string
  balance: string
  source?: string
}

export type InvoiceItem = { id: string; position: number; description: string; quantity: string; unit_price: string; line_total: string }

export type PaymentMethod = 'bank_transfer' | 'card' | 'cash' | 'check' | 'other'

export type Payment = {
  id: string
  invoice_id: string
  invoice_number?: string
  customer_id?: string
  customer_name?: string
  amount: string
  currency: string
  paid_on: string
  method: PaymentMethod
  reference: string | null
  status: 'completed' | 'reversed'
  recorded_by_name?: string | null
  reversed_at?: string | null
  reversal_reason?: string | null
  source: string
  created_at: string
}

export type Invoice = {
  id: string
  number: string
  status: InvoiceStatus
  display_status: DisplayStatus
  is_overdue: boolean
  customer_id: string
  customer_name: string
  customer_email?: string | null
  project_id: string | null
  project_name: string | null
  currency: string
  issue_date: string
  due_date: string
  discount_type: 'none' | 'percent' | 'fixed'
  discount_value: string
  tax_rate: string
  subtotal: string
  discount_total: string
  tax_total: string
  total: string
  amount_paid: string
  balance: string
  notes: string | null
  sent_at: string | null
  voided_at: string | null
  void_reason: string | null
  source: string
  external_id: string | null
  version: number
  created_at: string
  updated_at: string
  items: InvoiceItem[]
  payments: Payment[]
}

export type InvoicePreview = {
  currency: string
  line_totals: string[]
  subtotal: string
  discount_total: string
  taxable_amount: string
  tax_total: string
  total: string
}

export type ExpenseStatus = 'pending' | 'approved' | 'rejected'

export type Expense = {
  id: string
  vendor_name: string
  expense_date: string
  amount: string
  currency: string
  description: string | null
  approval_status: ExpenseStatus
  category_id: string
  category_name: string
  project_id: string | null
  project_name: string | null
  submitted_by: string | null
  submitted_by_name: string | null
  reviewed_by_name?: string | null
  reviewed_at?: string | null
  review_note?: string | null
  version?: number
  created_at: string
}

export type ExpenseCategory = { id: string; name: string; is_archived: boolean }
