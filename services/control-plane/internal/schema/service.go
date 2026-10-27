package schema

import (
	"context"
	"strings"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/project"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/requestid"
	"github.com/google/uuid"
)

type Service struct {
	repo        *Repository
	orgRepo     *org.Repository
	projectRepo *project.Repository
}

func NewService(repo *Repository, orgRepo *org.Repository, projectRepo *project.Repository) *Service {
	return &Service{repo: repo, orgRepo: orgRepo, projectRepo: projectRepo}
}

func (s *Service) requireMembership(ctx context.Context, orgID, userID uuid.UUID) (org.Membership, error) {
	m, err := s.orgRepo.GetActiveMembership(ctx, orgID, userID)
	if err != nil {
		return org.Membership{}, ErrNotFound
	}
	o, err := s.orgRepo.GetOrganization(ctx, orgID)
	if err != nil {
		return org.Membership{}, ErrNotFound
	}
	if o.Status == org.OrgStatusArchived {
		return org.Membership{}, ErrNotFound
	}
	return m, nil
}

func (s *Service) requireActiveChain(ctx context.Context, orgID, projectID, envID uuid.UUID) error {
	chain, err := s.repo.GetEnvironmentChain(ctx, orgID, projectID, envID)
	if err != nil {
		return err
	}
	if chain.OrgStatus != string(org.OrgStatusActive) {
		return ErrInactive
	}
	if chain.ProjectStatus != string(project.StatusActive) {
		return ErrInactive
	}
	if chain.EnvStatus != string(project.StatusActive) {
		return ErrInactive
	}
	return nil
}

func (s *Service) GetSchema(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID) (Draft, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Draft{}, err
	}
	if !m.Role.CanReadSchema() {
		return Draft{}, ErrForbidden
	}
	if err := s.requireActiveChain(ctx, orgID, projectID, envID); err != nil {
		return Draft{}, err
	}
	return s.repo.GetDraft(ctx, envID)
}

func (s *Service) PutSchema(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, in PutSchemaInput) (Draft, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Draft{}, err
	}
	if !m.Role.CanMutateSchema() {
		return Draft{}, ErrForbidden
	}
	if err := s.requireActiveChain(ctx, orgID, projectID, envID); err != nil {
		return Draft{}, err
	}

	if in.Document.Entities == nil {
		in.Document.Entities = map[string]EntityDefinition{}
	}

	current, err := s.repo.GetDraft(ctx, envID)
	if err != nil {
		return Draft{}, err
	}
	expected := current.Revision
	if in.ExpectedRevision != nil {
		expected = *in.ExpectedRevision
	}

	if err := s.enforceOwnershipInvariants(current.Document, in.Document); err != nil {
		return Draft{}, err
	}
	if err := ValidateDocument(in.Document); err != nil {
		return Draft{}, err
	}

	return s.repo.SaveDraft(
		ctx, envID, expected, in.Document,
		principal.UserID, requestid.FromContext(ctx), "schema.draft.put",
	)
}

func (s *Service) UpsertCustomEntity(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, expectedRevision int64, in EntityMutationInput) (Draft, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Draft{}, err
	}
	if !m.Role.CanMutateSchema() {
		return Draft{}, ErrForbidden
	}
	if err := s.requireActiveChain(ctx, orgID, projectID, envID); err != nil {
		return Draft{}, err
	}

	current, err := s.repo.GetDraft(ctx, envID)
	if err != nil {
		return Draft{}, err
	}
	if current.Revision != expectedRevision {
		return Draft{}, ErrConflictRevision
	}

	key := normalizeKey(in.Key)
	if err := validateEntityKey(key); err != nil {
		return Draft{}, err
	}
	display := strings.TrimSpace(in.DisplayName)
	if display == "" {
		return Draft{}, ErrInvalidInput
	}

	doc := cloneDocument(current.Document)
	now := time.Now().UTC()
	existing, exists := doc.Entities[key]
	if exists {
		if existing.Origin != OriginCustom || existing.Protected {
			return Draft{}, ErrProtected
		}
		existing.DisplayName = display
		existing.UpdatedAt = now
		doc.Entities[key] = existing
	} else {
		doc.Entities[key] = EntityDefinition{
			ID:            uuid.Must(uuid.NewV7()),
			Key:           key,
			DisplayName:   display,
			Origin:        OriginCustom,
			Protected:     false,
			Fields:        map[string]FieldDefinition{},
			Relationships: map[string]RelationshipDefinition{},
			CreatedAt:     now,
			UpdatedAt:     now,
		}
	}

	if err := ValidateDocument(doc); err != nil {
		return Draft{}, err
	}
	return s.repo.SaveDraft(ctx, envID, expectedRevision, doc, principal.UserID, requestid.FromContext(ctx), "schema.entity.upsert")
}

func (s *Service) DeleteCustomEntity(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, expectedRevision int64, entityKey string) (Draft, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Draft{}, err
	}
	if !m.Role.CanMutateSchema() {
		return Draft{}, ErrForbidden
	}
	if err := s.requireActiveChain(ctx, orgID, projectID, envID); err != nil {
		return Draft{}, err
	}

	current, err := s.repo.GetDraft(ctx, envID)
	if err != nil {
		return Draft{}, err
	}
	if current.Revision != expectedRevision {
		return Draft{}, ErrConflictRevision
	}

	key := normalizeKey(entityKey)
	ent, ok := current.Document.Entities[key]
	if !ok {
		return Draft{}, ErrNotFound
	}
	if ent.Origin != OriginCustom || ent.Protected {
		return Draft{}, ErrProtected
	}

	doc := cloneDocument(current.Document)
	delete(doc.Entities, key)
	for ek, e := range doc.Entities {
		for rk, rel := range e.Relationships {
			if normalizeKey(rel.TargetEntity) == key {
				delete(e.Relationships, rk)
			}
		}
		doc.Entities[ek] = e
	}

	if err := ValidateDocument(doc); err != nil {
		return Draft{}, err
	}
	return s.repo.SaveDraft(ctx, envID, expectedRevision, doc, principal.UserID, requestid.FromContext(ctx), "schema.entity.delete")
}

func (s *Service) UpsertCustomField(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, expectedRevision int64, entityKey string, in FieldMutationInput) (Draft, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Draft{}, err
	}
	if !m.Role.CanMutateSchema() {
		return Draft{}, ErrForbidden
	}
	if err := s.requireActiveChain(ctx, orgID, projectID, envID); err != nil {
		return Draft{}, err
	}

	current, err := s.repo.GetDraft(ctx, envID)
	if err != nil {
		return Draft{}, err
	}
	if current.Revision != expectedRevision {
		return Draft{}, ErrConflictRevision
	}

	ek := normalizeKey(entityKey)
	ent, ok := current.Document.Entities[ek]
	if !ok {
		return Draft{}, ErrUnknownEntity
	}
	if ent.Origin == OriginSystem {
		return Draft{}, ErrProtected
	}

	fk := normalizeKey(in.Key)
	now := time.Now().UTC()
	doc := cloneDocument(current.Document)
	ent = doc.Entities[ek]
	if ent.Fields == nil {
		ent.Fields = map[string]FieldDefinition{}
	}

	existing, exists := ent.Fields[fk]
	if exists {
		if existing.Origin != OriginCustom || existing.Protected {
			return Draft{}, ErrProtected
		}
		existing.DisplayName = strings.TrimSpace(in.DisplayName)
		existing.Type = in.Type
		existing.Required = in.Required
		existing.Nullable = in.Nullable
		existing.Default = in.Default
		existing.Constraints = in.Constraints
		existing.UpdatedAt = now
		ent.Fields[fk] = existing
	} else {
		ent.Fields[fk] = FieldDefinition{
			ID:          uuid.Must(uuid.NewV7()),
			Key:         fk,
			DisplayName: strings.TrimSpace(in.DisplayName),
			Type:        in.Type,
			Required:    in.Required,
			Nullable:    in.Nullable,
			Default:     in.Default,
			Constraints: in.Constraints,
			Origin:      OriginCustom,
			Protected:   false,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	}
	ent.UpdatedAt = now
	doc.Entities[ek] = ent

	if err := ValidateDocument(doc); err != nil {
		return Draft{}, err
	}
	return s.repo.SaveDraft(ctx, envID, expectedRevision, doc, principal.UserID, requestid.FromContext(ctx), "schema.field.upsert")
}

func (s *Service) DeleteCustomField(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, expectedRevision int64, entityKey, fieldKey string) (Draft, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Draft{}, err
	}
	if !m.Role.CanMutateSchema() {
		return Draft{}, ErrForbidden
	}
	if err := s.requireActiveChain(ctx, orgID, projectID, envID); err != nil {
		return Draft{}, err
	}

	current, err := s.repo.GetDraft(ctx, envID)
	if err != nil {
		return Draft{}, err
	}
	if current.Revision != expectedRevision {
		return Draft{}, ErrConflictRevision
	}

	ek := normalizeKey(entityKey)
	ent, ok := current.Document.Entities[ek]
	if !ok {
		return Draft{}, ErrUnknownEntity
	}
	fk := normalizeKey(fieldKey)
	f, ok := ent.Fields[fk]
	if !ok {
		return Draft{}, ErrNotFound
	}
	if f.Origin != OriginCustom || f.Protected {
		return Draft{}, ErrProtected
	}

	doc := cloneDocument(current.Document)
	ent = doc.Entities[ek]
	delete(ent.Fields, fk)
	ent.UpdatedAt = time.Now().UTC()
	doc.Entities[ek] = ent

	if err := ValidateDocument(doc); err != nil {
		return Draft{}, err
	}
	return s.repo.SaveDraft(ctx, envID, expectedRevision, doc, principal.UserID, requestid.FromContext(ctx), "schema.field.delete")
}

func (s *Service) UpsertCustomRelationship(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, expectedRevision int64, entityKey string, in RelationshipMutationInput) (Draft, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Draft{}, err
	}
	if !m.Role.CanMutateSchema() {
		return Draft{}, ErrForbidden
	}
	if err := s.requireActiveChain(ctx, orgID, projectID, envID); err != nil {
		return Draft{}, err
	}

	current, err := s.repo.GetDraft(ctx, envID)
	if err != nil {
		return Draft{}, err
	}
	if current.Revision != expectedRevision {
		return Draft{}, ErrConflictRevision
	}

	ek := normalizeKey(entityKey)
	ent, ok := current.Document.Entities[ek]
	if !ok {
		return Draft{}, ErrUnknownEntity
	}
	if ent.Origin == OriginSystem {
		return Draft{}, ErrProtected
	}

	rk := normalizeKey(in.Key)
	now := time.Now().UTC()
	doc := cloneDocument(current.Document)
	ent = doc.Entities[ek]
	if ent.Relationships == nil {
		ent.Relationships = map[string]RelationshipDefinition{}
	}

	existing, exists := ent.Relationships[rk]
	if exists {
		if existing.Origin != OriginCustom || existing.Protected {
			return Draft{}, ErrProtected
		}
		existing.SourceEntity = ek
		existing.TargetEntity = normalizeKey(in.TargetEntity)
		existing.Cardinality = in.Cardinality
		existing.Required = in.Required
		existing.DeleteBehavior = in.DeleteBehavior
		existing.UpdatedAt = now
		ent.Relationships[rk] = existing
	} else {
		ent.Relationships[rk] = RelationshipDefinition{
			ID:             uuid.Must(uuid.NewV7()),
			Key:            rk,
			SourceEntity:   ek,
			TargetEntity:   normalizeKey(in.TargetEntity),
			Cardinality:    in.Cardinality,
			Required:       in.Required,
			DeleteBehavior: in.DeleteBehavior,
			Origin:         OriginCustom,
			Protected:      false,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
	}
	ent.UpdatedAt = now
	doc.Entities[ek] = ent

	if err := ValidateDocument(doc); err != nil {
		return Draft{}, err
	}
	return s.repo.SaveDraft(ctx, envID, expectedRevision, doc, principal.UserID, requestid.FromContext(ctx), "schema.relationship.upsert")
}

func (s *Service) DeleteCustomRelationship(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, expectedRevision int64, entityKey, relKey string) (Draft, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Draft{}, err
	}
	if !m.Role.CanMutateSchema() {
		return Draft{}, ErrForbidden
	}
	if err := s.requireActiveChain(ctx, orgID, projectID, envID); err != nil {
		return Draft{}, err
	}

	current, err := s.repo.GetDraft(ctx, envID)
	if err != nil {
		return Draft{}, err
	}
	if current.Revision != expectedRevision {
		return Draft{}, ErrConflictRevision
	}

	ek := normalizeKey(entityKey)
	ent, ok := current.Document.Entities[ek]
	if !ok {
		return Draft{}, ErrUnknownEntity
	}
	rk := normalizeKey(relKey)
	rel, ok := ent.Relationships[rk]
	if !ok {
		return Draft{}, ErrNotFound
	}
	if rel.Origin != OriginCustom || rel.Protected {
		return Draft{}, ErrProtected
	}

	doc := cloneDocument(current.Document)
	ent = doc.Entities[ek]
	delete(ent.Relationships, rk)
	ent.UpdatedAt = time.Now().UTC()
	doc.Entities[ek] = ent

	if err := ValidateDocument(doc); err != nil {
		return Draft{}, err
	}
	return s.repo.SaveDraft(ctx, envID, expectedRevision, doc, principal.UserID, requestid.FromContext(ctx), "schema.relationship.delete")
}

func (s *Service) enforceOwnershipInvariants(prev, next SchemaDocument) error {
	for key, nextEnt := range next.Entities {
		prevEnt, ok := prev.Entities[key]
		if !ok {
			if nextEnt.Origin != OriginCustom {
				return ErrProtected
			}
			continue
		}
		if prevEnt.Origin != nextEnt.Origin {
			return ErrProtected
		}
		if prevEnt.Protected && !equalEntityCore(prevEnt, nextEnt) {
			return ErrProtected
		}
		if prevEnt.Origin == OriginSystem || prevEnt.Origin == OriginModule {
			if err := enforceProtectedFields(prevEnt, nextEnt); err != nil {
				return err
			}
		}
	}
	for key, prevEnt := range prev.Entities {
		if _, ok := next.Entities[key]; !ok {
			if prevEnt.Origin != OriginCustom || prevEnt.Protected {
				return ErrProtected
			}
		}
	}
	return nil
}

func equalEntityCore(a, b EntityDefinition) bool {
	return a.Origin == b.Origin &&
		a.ModuleSlug == b.ModuleSlug &&
		uuidPtrEqual(a.ReleaseID, b.ReleaseID) &&
		a.Protected == b.Protected
}

func uuidPtrEqual(a, b *uuid.UUID) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func enforceProtectedFields(prev, next EntityDefinition) error {
	for fk, pf := range prev.Fields {
		nf, ok := next.Fields[fk]
		if !ok {
			if pf.Protected || pf.Origin != OriginCustom {
				return ErrProtected
			}
			continue
		}
		if pf.Protected || pf.Origin != OriginCustom {
			if pf.Type != nf.Type || pf.Origin != nf.Origin || pf.Protected != nf.Protected {
				return ErrProtected
			}
		}
	}
	for rk, pr := range prev.Relationships {
		nr, ok := next.Relationships[rk]
		if !ok {
			if pr.Protected || pr.Origin != OriginCustom {
				return ErrProtected
			}
			continue
		}
		if pr.Protected || pr.Origin != OriginCustom {
			if pr.Cardinality != nr.Cardinality || pr.Origin != nr.Origin || pr.Protected != nr.Protected {
				return ErrProtected
			}
		}
	}
	return nil
}

func cloneDocument(doc SchemaDocument) SchemaDocument {
	out := SchemaDocument{Entities: make(map[string]EntityDefinition, len(doc.Entities))}
	for k, e := range doc.Entities {
		fields := make(map[string]FieldDefinition, len(e.Fields))
		for fk, f := range e.Fields {
			fields[fk] = f
		}
		rels := make(map[string]RelationshipDefinition, len(e.Relationships))
		for rk, r := range e.Relationships {
			rels[rk] = r
		}
		e.Fields = fields
		e.Relationships = rels
		out.Entities[k] = e
	}
	return out
}
