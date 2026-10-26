package org

import (
	"time"

	"github.com/google/uuid"
)

type OrgStatus string

const (
	OrgStatusActive    OrgStatus = "active"
	OrgStatusSuspended OrgStatus = "suspended"
	OrgStatusArchived  OrgStatus = "archived"
)

type MembershipStatus string

const (
	MembershipActive  MembershipStatus = "active"
	MembershipRemoved MembershipStatus = "removed"
)

type Organization struct {
	ID                      uuid.UUID
	Slug                    string
	DisplayName             string
	Status                  OrgStatus
	CreatedByIdentityUserID uuid.UUID
	CreatedAt               time.Time
	UpdatedAt               time.Time
	ArchivedAt              *time.Time
}

type Membership struct {
	OrganizationID uuid.UUID
	IdentityUserID uuid.UUID
	Role           Role
	Status         MembershipStatus
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CreateOrgInput struct {
	Slug        string
	DisplayName string
}

type ChangeRoleInput struct {
	Role Role
}
