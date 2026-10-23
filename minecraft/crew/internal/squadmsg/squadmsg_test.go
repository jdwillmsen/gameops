package squadmsg

import "testing"

func TestRankOrdersRolesForLeadership(t *testing.T) {
	t.Parallel()
	tests := []struct {
		role Role
		rank int
	}{
		{RoleVanguard, 0},
		{RoleStriker, 1},
		{RoleRanger, 2},
		{Role("goblin"), 3},
		{Role(""), 3},
	}
	for _, tt := range tests {
		if got := Rank(tt.role); got != tt.rank {
			t.Errorf("Rank(%q) = %d, want %d", tt.role, got, tt.rank)
		}
	}
}

func TestValidRoleAcceptsOnlyWarriorRoles(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		role  Role
		valid bool
	}{
		{RoleVanguard, true},
		{RoleStriker, true},
		{RoleRanger, true},
		{Role("worker"), false},
		{Role(""), false},
	} {
		if got := ValidRole(tt.role); got != tt.valid {
			t.Errorf("ValidRole(%q) = %v, want %v", tt.role, got, tt.valid)
		}
	}
}
