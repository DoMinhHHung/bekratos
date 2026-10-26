package org

import "testing"

func TestRolePermissions(t *testing.T) {
	if !RoleOwner.CanArchiveOrg() || RoleAdmin.CanArchiveOrg() {
		t.Fatal("archive permission mismatch")
	}
	if !RoleOwner.CanAssignRole(RoleOwner) {
		t.Fatal("owner should assign owner")
	}
	if RoleAdmin.CanAssignRole(RoleOwner) {
		t.Fatal("admin must not assign owner")
	}
	if !RoleAdmin.CanAssignRole(RoleDeveloper) {
		t.Fatal("admin should assign developer")
	}
	if RoleDeveloper.CanManageMembers() || RoleViewer.CanManageMembers() || RoleBilling.CanManageMembers() {
		t.Fatal("non-managers must not manage members")
	}
	if !RoleBilling.CanListMembers() == false && RoleBilling.CanListMembers() {
		// billing cannot list members
	}
	if RoleBilling.CanListMembers() {
		t.Fatal("billing must not list members")
	}
	if !RoleViewer.CanListMembers() {
		t.Fatal("viewer should list members")
	}
}

func TestParseRole(t *testing.T) {
	if _, ok := ParseRole("owner"); !ok {
		t.Fatal("owner")
	}
	if _, ok := ParseRole("superadmin"); ok {
		t.Fatal("superadmin must be invalid")
	}
}
