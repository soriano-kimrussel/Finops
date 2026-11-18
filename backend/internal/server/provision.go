package server

import (
	"context"

	"github.com/google/uuid"

	"finopshq/internal/ledger"
	"finopshq/internal/store"
)

var defaultExpenseCategories = []string{
	"Contractors", "Hardware", "Marketing", "Office & Facilities", "Professional Services", "Software & Subscriptions", "Travel", "Other",
}

// ProvisionOrganization seeds per-organization defaults inside the creating transaction.
func ProvisionOrganization(ctx context.Context, q *store.Queries, orgID uuid.UUID) error {
	for _, a := range ledger.DefaultAccounts {
		if err := q.CreateAccount(ctx, store.CreateAccountParams{OrganizationID: orgID, Code: a.Code, Name: a.Name, Type: a.Type}); err != nil {
			return err
		}
	}
	if err := q.CreateInvoiceSequence(ctx, orgID); err != nil {
		return err
	}
	for _, name := range defaultExpenseCategories {
		if _, err := q.CreateExpenseCategory(ctx, store.CreateExpenseCategoryParams{OrganizationID: orgID, Name: name}); err != nil {
			return err
		}
	}
	return nil
}
