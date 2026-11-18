package authz

import "testing"

func TestRolePermissionMatrix(t *testing.T) {
	cases := []struct {
		role Role
		perm Permission
		want bool
	}{
		{RoleOwner, MembersManage, true},
		{RoleAdmin, IntegrationsManage, true},
		{RoleAccountant, PaymentsRecord, true},
		{RoleAccountant, ExpensesApprove, true},
		{RoleAccountant, MembersManage, false},
		{RoleAccountant, ProjectsManage, false},
		{RoleProjectManager, ProjectsManage, true},
		{RoleProjectManager, BudgetsManage, true},
		{RoleProjectManager, InvoicesManage, false},
		{RoleProjectManager, PaymentsRecord, false},
		{RoleProjectManager, ExpensesApprove, false},
		{RoleProjectManager, AuditView, false},
		{RoleViewer, InvoicesView, true},
		{RoleViewer, ExpensesSubmit, false},
		{RoleViewer, AuditView, false},
		{Role("intruder"), InvoicesView, false},
	}
	for _, c := range cases {
		if got := c.role.Can(c.perm); got != c.want {
			t.Errorf("%s.Can(%s) = %v, want %v", c.role, c.perm, got, c.want)
		}
	}
}

func TestRoleValid(t *testing.T) {
	for _, r := range []Role{RoleOwner, RoleAdmin, RoleAccountant, RoleProjectManager, RoleViewer} {
		if !r.Valid() {
			t.Errorf("%s should be valid", r)
		}
	}
	if Role("superuser").Valid() {
		t.Error("unknown role must be invalid")
	}
}
