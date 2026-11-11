package repo

import (
	"context"
	"testing"
)

func TestCanManageCompanyFailsClosed(t *testing.T) {
	if CanManageCompany(context.Background()) || CanSuperviseCompany(context.Background()) {
		t.Error("a request without an agent must not see management UI")
	}
	for _, tc := range []struct {
		role               string
		manage, supervise bool
	}{
		{RoleAgent, false, false},
		{RoleSupervisor, false, true},
		{RoleAdmin, true, true},
	} {
		ctx := ContextWithAgent(context.Background(), Agent{Role: tc.role})
		if CanManageCompany(ctx) != tc.manage || CanSuperviseCompany(ctx) != tc.supervise {
			t.Errorf("%s: manage=%v supervise=%v", tc.role, CanManageCompany(ctx), CanSuperviseCompany(ctx))
		}
		if !validAgentRole(tc.role) {
			t.Errorf("%s must be a valid role", tc.role)
		}
	}
	if validAgentRole("owner") {
		t.Error("unknown roles stay invalid")
	}
}
