package org

type Role string

const (
	RoleOwner     Role = "owner"
	RoleAdmin     Role = "admin"
	RoleDeveloper Role = "developer"
	RoleViewer    Role = "viewer"
	RoleBilling   Role = "billing"
)

func ParseRole(s string) (Role, bool) {
	switch Role(s) {
	case RoleOwner, RoleAdmin, RoleDeveloper, RoleViewer, RoleBilling:
		return Role(s), true
	default:
		return "", false
	}
}

func (r Role) CanArchiveOrg() bool {
	return r == RoleOwner
}

func (r Role) CanListMembers() bool {
	return r == RoleOwner || r == RoleAdmin || r == RoleDeveloper || r == RoleViewer
}

func (r Role) CanManageMembers() bool {
	return r == RoleOwner || r == RoleAdmin
}

func (r Role) CanAssignRole(target Role) bool {
	if r == RoleOwner {
		return true
	}
	if r == RoleAdmin {
		return target != RoleOwner
	}
	return false
}

func (r Role) CanModifyMember(targetRole Role) bool {
	if r == RoleOwner {
		return true
	}
	if r == RoleAdmin {
		return targetRole != RoleOwner
	}
	return false
}
