package org

import "testing"

func TestProjectPermissions(t *testing.T) {
	if !RoleDeveloper.CanCreateProject() || RoleViewer.CanCreateProject() || RoleBilling.CanCreateProject() {
		t.Fatal("create project matrix")
	}
	if !RoleViewer.CanReadProject() || RoleBilling.CanReadProject() {
		t.Fatal("read project matrix")
	}
	if RoleDeveloper.CanArchiveProject() || !RoleAdmin.CanArchiveProject() {
		t.Fatal("archive project matrix")
	}
	if !RoleDeveloper.CanCreateEnvironment() || RoleViewer.CanCreateEnvironment() {
		t.Fatal("create env matrix")
	}
	if RoleDeveloper.CanArchiveEnvironment() || !RoleOwner.CanArchiveEnvironment() {
		t.Fatal("archive env matrix")
	}
}
