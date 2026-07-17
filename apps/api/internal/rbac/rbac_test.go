package rbac

import "testing"

func TestCanPermissionMatrix(t *testing.T) {
	cases := []struct {
		role string
		perm Permission
		want bool
	}{
		{RoleMember, PermOrgView, true},
		{RoleMember, PermMemberList, true},
		{RoleMember, PermOrgUpdate, false},
		{RoleMember, PermOrgDelete, false},
		{RoleMember, PermMemberInvite, false},
		{RoleMember, PermMemberManage, false},

		{RoleAdmin, PermOrgView, true},
		{RoleAdmin, PermOrgUpdate, true},
		{RoleAdmin, PermMemberInvite, true},
		{RoleAdmin, PermMemberManage, true},
		{RoleAdmin, PermOrgDelete, false}, // only owners delete the org

		{RoleOwner, PermOrgDelete, true},
		{RoleOwner, PermMemberManage, true},

		{"nonsense", PermOrgView, false},
	}
	for _, c := range cases {
		if got := Can(c.role, c.perm); got != c.want {
			t.Errorf("Can(%q,%q)=%v want %v", c.role, c.perm, got, c.want)
		}
	}
}

// TestCanManageMembershipMatrix is the executable privilege-escalation spec:
// for every (actor, target, newRole) it pins allow/deny.
func TestCanManageMembershipMatrix(t *testing.T) {
	cases := []struct {
		name                   string
		actor, target, newRole string
		want                   bool
	}{
		{"member cannot manage anyone", RoleMember, RoleMember, RoleAdmin, false},
		{"admin promotes member to admin", RoleAdmin, RoleMember, RoleAdmin, true},
		{"admin CANNOT promote to owner", RoleAdmin, RoleMember, RoleOwner, false},
		{"admin CANNOT modify an owner", RoleAdmin, RoleOwner, RoleMember, false},
		{"admin removes a member", RoleAdmin, RoleMember, "", true},
		{"admin CANNOT remove an owner", RoleAdmin, RoleOwner, "", false},
		{"owner promotes to owner", RoleOwner, RoleMember, RoleOwner, true},
		{"owner demotes another owner", RoleOwner, RoleOwner, RoleAdmin, true},
		{"owner removes an owner", RoleOwner, RoleOwner, "", true},
	}
	for _, c := range cases {
		if got := CanManageMembership(c.actor, c.target, c.newRole); got != c.want {
			t.Errorf("%s: CanManageMembership(%q,%q,%q)=%v want %v",
				c.name, c.actor, c.target, c.newRole, got, c.want)
		}
	}
}

// TestSiteManagePermGrants is the S8.1 site:manage deliberate-red: a non-holder (member) is refused
// by construction (the grant table), owner+admin hold it, and it is mutating (email-gated at the
// handler). The handler 403 rides authorize(..., PermSiteManage) — this pins the grants that back it.
func TestSiteManagePermGrants(t *testing.T) {
	if Can(RoleMember, PermSiteManage) {
		t.Fatal("a member must NOT hold site:manage (register/bind/advertise/approve are admin powers)")
	}
	if !Can(RoleAdmin, PermSiteManage) || !Can(RoleOwner, PermSiteManage) {
		t.Fatal("owner and admin must hold site:manage")
	}
	if !IsMutating(PermSiteManage) {
		t.Fatal("site:manage is mutating (must be email-verified gated)")
	}
}
