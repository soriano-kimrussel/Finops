-- Revenue is recognised on the invoice issue date (accrual basis) and excludes tax:
-- subtotal − discount of every sent, partially paid or paid invoice. Drafts and voids never count.
-- Expenses are approved expenses on their expense date. Cash figures come from the ledger.
-- All aggregates are restricted to one currency; nothing is converted.

-- name: DashboardTotals :one
SELECT
    (SELECT coalesce(sum(i.subtotal - i.discount_total), 0) FROM invoices i
      WHERE i.status IN ('sent', 'partially_paid', 'paid') AND i.currency = sqlc.arg('currency')
        AND i.issue_date BETWEEN sqlc.arg('period_start')::date AND sqlc.arg('period_end')::date)::numeric AS revenue,
    (SELECT coalesce(sum(e.amount), 0) FROM expenses e
      WHERE e.approval_status = 'approved' AND e.currency = sqlc.arg('currency')
        AND e.expense_date BETWEEN sqlc.arg('period_start')::date AND sqlc.arg('period_end')::date)::numeric AS expenses,
    (SELECT coalesce(sum(t.amount), 0) FROM transactions t
      WHERE t.direction = 'in' AND t.currency = sqlc.arg('currency') AND t.occurred_on BETWEEN sqlc.arg('period_start')::date AND sqlc.arg('period_end')::date)::numeric AS cash_in,
    (SELECT coalesce(sum(t.amount), 0) FROM transactions t
      WHERE t.direction = 'out' AND t.currency = sqlc.arg('currency') AND t.occurred_on BETWEEN sqlc.arg('period_start')::date AND sqlc.arg('period_end')::date)::numeric AS cash_out,
    (SELECT count(*) FROM invoices i WHERE i.status IN ('sent', 'partially_paid') AND i.currency = sqlc.arg('currency'))::bigint AS outstanding_count,
    (SELECT coalesce(sum(i.total - i.amount_paid), 0) FROM invoices i
      WHERE i.status IN ('sent', 'partially_paid') AND i.currency = sqlc.arg('currency'))::numeric AS outstanding_amount,
    (SELECT count(*) FROM invoices i
      WHERE i.status IN ('sent', 'partially_paid') AND i.currency = sqlc.arg('currency') AND i.due_date < sqlc.arg('today')::date)::bigint AS overdue_count,
    (SELECT coalesce(sum(i.total - i.amount_paid), 0) FROM invoices i
      WHERE i.status IN ('sent', 'partially_paid') AND i.currency = sqlc.arg('currency') AND i.due_date < sqlc.arg('today')::date)::numeric AS overdue_amount,
    ((SELECT count(*) FROM invoices i WHERE i.currency <> sqlc.arg('currency') AND i.status NOT IN ('draft', 'void')
        AND i.issue_date BETWEEN sqlc.arg('period_start')::date AND sqlc.arg('period_end')::date)
     + (SELECT count(*) FROM expenses e WHERE e.currency <> sqlc.arg('currency') AND e.approval_status = 'approved'
        AND e.expense_date BETWEEN sqlc.arg('period_start')::date AND sqlc.arg('period_end')::date))::bigint AS other_currency_records;

-- name: MonthlyFinancials :many
WITH months AS (
    SELECT generate_series(date_trunc('month', sqlc.arg('period_start')::date), date_trunc('month', sqlc.arg('period_end')::date), interval '1 month')::date AS month
),
rev AS (
    SELECT date_trunc('month', i.issue_date)::date AS m, sum(i.subtotal - i.discount_total) AS revenue, sum(i.total) AS invoiced
    FROM invoices i
    WHERE i.status IN ('sent', 'partially_paid', 'paid') AND i.currency = sqlc.arg('currency')::char(3)
      AND i.issue_date BETWEEN sqlc.arg('period_start')::date AND sqlc.arg('period_end')::date
    GROUP BY 1
),
exp AS (
    SELECT date_trunc('month', e.expense_date)::date AS m, sum(e.amount) AS expenses
    FROM expenses e
    WHERE e.approval_status = 'approved' AND e.currency = sqlc.arg('currency')::char(3)
      AND e.expense_date BETWEEN sqlc.arg('period_start')::date AND sqlc.arg('period_end')::date
    GROUP BY 1
),
cash AS (
    SELECT date_trunc('month', t.occurred_on)::date AS m,
           sum(t.amount) FILTER (WHERE t.direction = 'in') AS cash_in,
           sum(t.amount) FILTER (WHERE t.direction = 'out') AS cash_out,
           sum(CASE WHEN t.source_type = 'payment' THEN t.amount WHEN t.source_type = 'payment_reversal' THEN -t.amount ELSE 0 END) AS collected
    FROM transactions t
    WHERE t.currency = sqlc.arg('currency')::char(3) AND t.occurred_on BETWEEN sqlc.arg('period_start')::date AND sqlc.arg('period_end')::date
    GROUP BY 1
)
SELECT months.month,
       coalesce(rev.revenue, 0)::numeric AS revenue,
       coalesce(rev.invoiced, 0)::numeric AS invoiced,
       coalesce(exp.expenses, 0)::numeric AS expenses,
       coalesce(cash.cash_in, 0)::numeric AS cash_in,
       coalesce(cash.cash_out, 0)::numeric AS cash_out,
       coalesce(cash.collected, 0)::numeric AS collected
FROM months
LEFT JOIN rev ON rev.m = months.month
LEFT JOIN exp ON exp.m = months.month
LEFT JOIN cash ON cash.m = months.month
ORDER BY months.month;

-- name: CashBalanceBefore :one
SELECT coalesce(sum(CASE direction WHEN 'in' THEN amount ELSE -amount END), 0)::numeric AS balance
FROM transactions WHERE currency = sqlc.arg('currency') AND occurred_on < sqlc.arg('before_date')::date;

-- name: RevenueByCustomer :many
SELECT c.id, c.name,
       count(*)::bigint AS invoice_count,
       sum(i.subtotal - i.discount_total)::numeric AS revenue,
       sum(i.tax_total)::numeric AS tax,
       sum(i.amount_paid)::numeric AS collected
FROM invoices i JOIN customers c ON c.id = i.customer_id
WHERE i.status IN ('sent', 'partially_paid', 'paid') AND i.currency = sqlc.arg('currency')
  AND i.issue_date BETWEEN sqlc.arg('period_start')::date AND sqlc.arg('period_end')::date
GROUP BY c.id, c.name
ORDER BY revenue DESC, c.name;

-- name: ExpensesByCategory :many
SELECT ec.id, ec.name, count(*)::bigint AS expense_count, sum(e.amount)::numeric AS total
FROM expenses e JOIN expense_categories ec ON ec.id = e.category_id
WHERE e.approval_status = 'approved' AND e.currency = sqlc.arg('currency') AND e.expense_date BETWEEN sqlc.arg('period_start')::date AND sqlc.arg('period_end')::date
GROUP BY ec.id, ec.name
ORDER BY total DESC, ec.name;

-- name: ExpensesByVendor :many
SELECT v.name AS vendor_name, count(*)::bigint AS expense_count, sum(e.amount)::numeric AS total
FROM expenses e JOIN vendors v ON v.id = e.vendor_id
WHERE e.approval_status = 'approved' AND e.currency = sqlc.arg('currency') AND e.expense_date BETWEEN sqlc.arg('period_start')::date AND sqlc.arg('period_end')::date
GROUP BY v.name
ORDER BY total DESC, v.name
LIMIT 25;

-- name: OpenInvoices :many
SELECT i.id, i.number, i.issue_date, i.due_date, i.total, i.amount_paid, (i.total - i.amount_paid)::numeric AS balance, i.status,
       c.id AS customer_id, c.name AS customer_name,
       greatest(sqlc.arg('as_of')::date - i.due_date, 0)::int AS days_overdue
FROM invoices i JOIN customers c ON c.id = i.customer_id
WHERE i.status IN ('sent', 'partially_paid') AND i.currency = sqlc.arg('currency')
ORDER BY i.due_date, i.number;

-- name: AgingByCustomer :many
SELECT c.id, c.name,
       coalesce(sum(i.total - i.amount_paid) FILTER (WHERE i.due_date >= sqlc.arg('as_of')::date), 0)::numeric AS current_amount,
       coalesce(sum(i.total - i.amount_paid) FILTER (WHERE sqlc.arg('as_of')::date - i.due_date BETWEEN 1 AND 30), 0)::numeric AS days_1_30,
       coalesce(sum(i.total - i.amount_paid) FILTER (WHERE sqlc.arg('as_of')::date - i.due_date BETWEEN 31 AND 60), 0)::numeric AS days_31_60,
       coalesce(sum(i.total - i.amount_paid) FILTER (WHERE sqlc.arg('as_of')::date - i.due_date BETWEEN 61 AND 90), 0)::numeric AS days_61_90,
       coalesce(sum(i.total - i.amount_paid) FILTER (WHERE sqlc.arg('as_of')::date - i.due_date > 90), 0)::numeric AS days_over_90,
       sum(i.total - i.amount_paid)::numeric AS total
FROM invoices i JOIN customers c ON c.id = i.customer_id
WHERE i.status IN ('sent', 'partially_paid') AND i.currency = sqlc.arg('currency')
GROUP BY c.id, c.name
ORDER BY total DESC, c.name;

-- name: BudgetedProjectsSummary :many
SELECT p.id, p.name, p.status,
       coalesce(b.total, 0)::numeric AS budget_total,
       coalesce(e.approved, 0)::numeric AS actual_total,
       coalesce(e.pending, 0)::numeric AS pending_total
FROM projects p
JOIN LATERAL (SELECT sum(amount) AS total FROM budgets WHERE project_id = p.id) b ON b.total IS NOT NULL
LEFT JOIN LATERAL (
    SELECT sum(amount) FILTER (WHERE approval_status = 'approved') AS approved,
           sum(amount) FILTER (WHERE approval_status = 'pending') AS pending
    FROM expenses WHERE project_id = p.id
) e ON true
WHERE p.currency = sqlc.arg('currency')
ORDER BY (coalesce(e.approved, 0) / b.total) DESC, p.name
LIMIT sqlc.arg('row_limit');
