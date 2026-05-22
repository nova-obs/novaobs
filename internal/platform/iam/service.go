package iam

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	platformrbac "novaobs/internal/platform/rbac"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrPermissionDenied = errors.New("permission_denied")
	ErrInvalidRequest   = errors.New("invalid_request")
	ErrNotFound         = errors.New("not_found")
)

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type Service struct {
	repo       Repository
	rbacRepo   RBACRepository
	authorizer Authorizer
}

func NewService(repo Repository, rbacRepo RBACRepository, authorizer Authorizer) Service {
	if authorizer == nil {
		authorizer = denyAuthorizer{}
	}
	return Service{repo: repo, rbacRepo: rbacRepo, authorizer: authorizer}
}

func (s Service) Me(ctx context.Context, actor platformrbac.Subject) (SubjectView, error) {
	if actor.ID == "" || actor.Type == "" {
		return SubjectView{}, ErrPermissionDenied
	}
	if actor.Type == SubjectTypeUser {
		if user, err := s.repo.GetUser(ctx, actor.ID); err == nil {
			user.PasswordSet = user.PasswordHash != ""
			return SubjectView{
				ID:          subjectID(SubjectTypeUser, user.ID),
				SubjectID:   user.ID,
				SubjectType: SubjectTypeUser,
				DisplayName: firstNonEmpty(user.DisplayName, user.Username, user.ID),
				Email:       user.Email,
				Status:      user.Status,
				Source:      user.Source,
				CreatedAt:   user.CreatedAt,
				UpdatedAt:   user.UpdatedAt,
			}, nil
		}
	}
	subjects, err := s.Subjects(ctx, actor)
	if err != nil {
		return SubjectView{}, err
	}
	id := subjectID(actor.Type, actor.ID)
	for _, subject := range subjects {
		if subject.ID == id {
			return subject, nil
		}
	}
	now := time.Now().UTC()
	return SubjectView{
		ID:          id,
		SubjectID:   actor.ID,
		SubjectType: actor.Type,
		DisplayName: firstNonEmpty(actor.DisplayName, actor.ID),
		Status:      "active",
		Source:      "request",
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (s Service) ListUsers(ctx context.Context, actor platformrbac.Subject) ([]User, error) {
	if !s.allowed(actor, "platform.user", "read") {
		return nil, ErrPermissionDenied
	}
	users, err := s.repo.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	for index := range users {
		users[index].PasswordSet = users[index].PasswordHash != ""
	}
	sort.SliceStable(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	return users, nil
}

func (s Service) CreateUser(ctx context.Context, actor platformrbac.Subject, req CreateUserRequest) (WriteResult[User], error) {
	if !s.allowed(actor, "platform.user", "manage") {
		return WriteResult[User]{}, ErrPermissionDenied
	}
	item := normalizeUser(req)
	if item.ID == "" || item.DisplayName == "" {
		return WriteResult[User]{}, ErrInvalidRequest
	}
	if existing, err := s.repo.GetUser(ctx, item.ID); err == nil {
		item.CreatedAt = existing.CreatedAt
		item.PasswordHash = existing.PasswordHash
	}
	if strings.TrimSpace(req.Password) != "" {
		passwordHash, err := HashPassword(req.Password)
		if err != nil {
			return WriteResult[User]{}, err
		}
		item.PasswordHash = passwordHash
	}
	if err := s.repo.SaveUser(ctx, item); err != nil {
		return WriteResult[User]{}, err
	}
	item.PasswordSet = item.PasswordHash != ""
	return WriteResult[User]{Item: item, Status: "created"}, nil
}

func (s Service) ListGroups(ctx context.Context, actor platformrbac.Subject) ([]Group, error) {
	if !s.allowed(actor, "platform.group", "read") {
		return nil, ErrPermissionDenied
	}
	groups, err := s.repo.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	memberships, err := s.repo.ListMemberships(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, item := range memberships {
		counts[item.GroupID]++
	}
	for index := range groups {
		groups[index].MemberCount = counts[groups[index].ID]
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	return groups, nil
}

func (s Service) CreateGroup(ctx context.Context, actor platformrbac.Subject, req CreateGroupRequest) (WriteResult[Group], error) {
	if !s.allowed(actor, "platform.group", "manage") {
		return WriteResult[Group]{}, ErrPermissionDenied
	}
	item := normalizeGroup(req)
	if item.ID == "" || item.DisplayName == "" {
		return WriteResult[Group]{}, ErrInvalidRequest
	}
	if err := s.repo.SaveGroup(ctx, item); err != nil {
		return WriteResult[Group]{}, err
	}
	return WriteResult[Group]{Item: item, Status: "created"}, nil
}

func (s Service) ListServiceAccounts(ctx context.Context, actor platformrbac.Subject) ([]ServiceAccount, error) {
	if !s.allowed(actor, "platform.service-account", "read") {
		return nil, ErrPermissionDenied
	}
	items, err := s.repo.ListServiceAccounts(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (s Service) CreateServiceAccount(ctx context.Context, actor platformrbac.Subject, req CreateServiceAccountRequest) (WriteResult[ServiceAccount], error) {
	if !s.allowed(actor, "platform.service-account", "manage") {
		return WriteResult[ServiceAccount]{}, ErrPermissionDenied
	}
	item := normalizeServiceAccount(req)
	if item.ID == "" || item.DisplayName == "" {
		return WriteResult[ServiceAccount]{}, ErrInvalidRequest
	}
	if err := s.repo.SaveServiceAccount(ctx, item); err != nil {
		return WriteResult[ServiceAccount]{}, err
	}
	return WriteResult[ServiceAccount]{Item: item, Status: "created"}, nil
}

func (s Service) Subjects(ctx context.Context, actor platformrbac.Subject) ([]SubjectView, error) {
	if !s.allowed(actor, "platform.subject", "read") {
		return nil, ErrPermissionDenied
	}
	return s.SubjectDirectory(ctx)
}

func (s Service) SubjectDirectory(ctx context.Context) ([]SubjectView, error) {
	users, err := s.repo.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := s.repo.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	serviceAccounts, err := s.repo.ListServiceAccounts(ctx)
	if err != nil {
		return nil, err
	}
	bindings, err := s.rbacRepo.ListBindings(ctx)
	if err != nil {
		return nil, err
	}
	refs := map[string]int{}
	derived := map[string]SubjectView{}
	for _, binding := range bindings {
		id := subjectID(binding.SubjectType, binding.SubjectID)
		if id == "" {
			continue
		}
		refs[id]++
		item, ok := derived[id]
		if !ok {
			item = SubjectView{
				ID:          id,
				SubjectID:   binding.SubjectID,
				SubjectType: binding.SubjectType,
				DisplayName: binding.SubjectID,
				Status:      "active",
				Source:      "binding",
				CreatedAt:   binding.CreatedAt,
				UpdatedAt:   binding.UpdatedAt,
			}
		}
		if item.UpdatedAt.Before(binding.UpdatedAt) {
			item.UpdatedAt = binding.UpdatedAt
		}
		derived[id] = item
	}
	out := make([]SubjectView, 0, len(users)+len(groups)+len(serviceAccounts))
	seen := map[string]bool{}
	for _, item := range users {
		view := SubjectView{
			ID:          subjectID(SubjectTypeUser, item.ID),
			SubjectID:   item.ID,
			SubjectType: SubjectTypeUser,
			DisplayName: firstNonEmpty(item.DisplayName, item.Username, item.ID),
			Email:       item.Email,
			Status:      item.Status,
			Source:      item.Source,
			CreatedAt:   item.CreatedAt,
			UpdatedAt:   item.UpdatedAt,
		}
		view.BindingRefs = refs[view.ID]
		out = append(out, view)
		seen[view.ID] = true
	}
	for _, item := range groups {
		view := SubjectView{
			ID:          subjectID(SubjectTypeGroup, item.ID),
			SubjectID:   item.ID,
			SubjectType: SubjectTypeGroup,
			DisplayName: firstNonEmpty(item.DisplayName, item.Name, item.ID),
			Status:      item.Status,
			Source:      item.Source,
			CreatedAt:   item.CreatedAt,
			UpdatedAt:   item.UpdatedAt,
		}
		view.BindingRefs = refs[view.ID]
		out = append(out, view)
		seen[view.ID] = true
	}
	for _, item := range serviceAccounts {
		view := SubjectView{
			ID:          subjectID(SubjectTypeServiceAccount, item.ID),
			SubjectID:   item.ID,
			SubjectType: SubjectTypeServiceAccount,
			DisplayName: firstNonEmpty(item.DisplayName, item.Name, item.ID),
			Status:      item.Status,
			Source:      "local",
			CreatedAt:   item.CreatedAt,
			UpdatedAt:   item.UpdatedAt,
		}
		view.BindingRefs = refs[view.ID]
		out = append(out, view)
		seen[view.ID] = true
	}
	for id, item := range derived {
		if seen[id] {
			continue
		}
		item.BindingRefs = refs[id]
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SubjectType == out[j].SubjectType {
			return out[i].SubjectID < out[j].SubjectID
		}
		return out[i].SubjectType < out[j].SubjectType
	})
	return out, nil
}

func HashPassword(password string) (string, error) {
	password = strings.TrimSpace(password)
	if len(password) < 8 {
		return "", ErrInvalidRequest
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func VerifyPassword(passwordHash string, password string) bool {
	if strings.TrimSpace(passwordHash) == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) == nil
}

func (s Service) ListRoles(ctx context.Context, actor platformrbac.Subject) ([]platformrbac.Role, error) {
	if !s.allowed(actor, "platform.role", "read") {
		return nil, ErrPermissionDenied
	}
	roles, err := s.rbacRepo.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(roles, func(i, j int) bool { return roles[i].ID < roles[j].ID })
	return roles, nil
}

func (s Service) CreateRole(ctx context.Context, actor platformrbac.Subject, req CreateRoleRequest) (WriteResult[platformrbac.Role], error) {
	if !s.allowed(actor, "platform.role", "manage") {
		return WriteResult[platformrbac.Role]{}, ErrPermissionDenied
	}
	role := normalizeRole(req)
	if role.ID == "" || role.Name == "" || len(role.Permissions) == 0 {
		return WriteResult[platformrbac.Role]{}, ErrInvalidRequest
	}
	if err := s.rbacRepo.SaveRole(role); err != nil {
		return WriteResult[platformrbac.Role]{}, err
	}
	return WriteResult[platformrbac.Role]{Item: role, Status: "created"}, nil
}

func (s Service) ListBindings(ctx context.Context, actor platformrbac.Subject) ([]BindingView, error) {
	if !s.allowed(actor, "platform.binding", "read") {
		return nil, ErrPermissionDenied
	}
	bindings, err := s.rbacRepo.ListBindings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BindingView, 0, len(bindings))
	for _, item := range bindings {
		role, _ := s.rbacRepo.GetRole(item.RoleID)
		out = append(out, BindingView{
			ID:          item.ID,
			SubjectID:   item.SubjectID,
			SubjectType: item.SubjectType,
			RoleID:      item.RoleID,
			RoleName:    role.Name,
			Scope:       item.Scope,
			CreatedAt:   item.CreatedAt,
			UpdatedAt:   item.UpdatedAt,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s Service) CreateBinding(ctx context.Context, actor platformrbac.Subject, req CreateBindingRequest) (WriteResult[BindingView], error) {
	if !s.allowed(actor, "platform.binding", "manage") {
		return WriteResult[BindingView]{}, ErrPermissionDenied
	}
	subjectType := normalizeSubjectType(req.SubjectType)
	subjectIDValue := strings.TrimSpace(req.SubjectID)
	roleID := strings.TrimSpace(req.RoleID)
	if subjectIDValue == "" || subjectType == "" || roleID == "" {
		return WriteResult[BindingView]{}, ErrInvalidRequest
	}
	role, err := s.rbacRepo.GetRole(roleID)
	if err != nil {
		return WriteResult[BindingView]{}, ErrNotFound
	}
	now := time.Now().UTC()
	binding := platformrbac.Binding{
		ID:          bindingID(subjectType, subjectIDValue, roleID, req.Scope),
		SubjectID:   subjectIDValue,
		SubjectType: subjectType,
		RoleID:      roleID,
		Scope:       req.Scope,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.rbacRepo.SaveBinding(binding); err != nil {
		return WriteResult[BindingView]{}, err
	}
	view := BindingView{
		ID:          binding.ID,
		SubjectID:   binding.SubjectID,
		SubjectType: binding.SubjectType,
		RoleID:      binding.RoleID,
		RoleName:    role.Name,
		Scope:       binding.Scope,
		CreatedAt:   binding.CreatedAt,
		UpdatedAt:   binding.UpdatedAt,
	}
	return WriteResult[BindingView]{Item: view, Status: "created"}, nil
}

func (s Service) allowed(actor platformrbac.Subject, resource string, action string) bool {
	if actor.ID == "" || actor.Type == "" {
		return false
	}
	readAction := action == "read" && s.authorizer.Authorize(actor, platformrbac.Request{
		Resource: "platform.iam",
		Action:   "read",
		Scope:    platformrbac.Scope{Global: true},
	}).Allowed
	if readAction {
		return true
	}
	manageAction := s.authorizer.Authorize(actor, platformrbac.Request{
		Resource: "platform.iam",
		Action:   "manage",
		Scope:    platformrbac.Scope{Global: true},
	}).Allowed
	if manageAction {
		return true
	}
	return s.authorizer.Authorize(actor, platformrbac.Request{
		Resource: resource,
		Action:   action,
		Scope:    platformrbac.Scope{Global: true},
	}).Allowed
}

func normalizeUser(req CreateUserRequest) User {
	now := time.Now().UTC()
	id := strings.TrimSpace(req.Username)
	return User{
		ID:          id,
		Username:    id,
		DisplayName: strings.TrimSpace(req.DisplayName),
		Email:       strings.TrimSpace(req.Email),
		Status:      firstNonEmpty(strings.TrimSpace(req.Status), "active"),
		Source:      "local",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func normalizeGroup(req CreateGroupRequest) Group {
	now := time.Now().UTC()
	id := strings.TrimSpace(req.Name)
	return Group{
		ID:          id,
		Name:        id,
		DisplayName: strings.TrimSpace(req.DisplayName),
		Description: strings.TrimSpace(req.Description),
		Status:      firstNonEmpty(strings.TrimSpace(req.Status), "active"),
		Source:      "local",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func normalizeServiceAccount(req CreateServiceAccountRequest) ServiceAccount {
	now := time.Now().UTC()
	id := strings.TrimSpace(req.Name)
	return ServiceAccount{
		ID:          id,
		Name:        id,
		DisplayName: strings.TrimSpace(req.DisplayName),
		Description: strings.TrimSpace(req.Description),
		Owner:       strings.TrimSpace(req.Owner),
		Status:      firstNonEmpty(strings.TrimSpace(req.Status), "active"),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func normalizeRole(req CreateRoleRequest) platformrbac.Role {
	now := time.Now().UTC()
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = roleID(req.Name, req.Permissions)
	}
	return platformrbac.Role{
		ID:          id,
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		Permissions: req.Permissions,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func subjectID(subjectType string, id string) string {
	subjectType = normalizeSubjectType(subjectType)
	id = strings.TrimSpace(id)
	if subjectType == "" || id == "" {
		return ""
	}
	return fmt.Sprintf("%s:%s", subjectType, id)
}

func normalizeSubjectType(value string) string {
	switch strings.TrimSpace(value) {
	case SubjectTypeUser:
		return SubjectTypeUser
	case SubjectTypeGroup:
		return SubjectTypeGroup
	case SubjectTypeServiceAccount:
		return SubjectTypeServiceAccount
	default:
		return ""
	}
}

func bindingID(subjectType string, subjectIDValue string, roleID string, scope platformrbac.Scope) string {
	raw := fmt.Sprintf("%s|%s|%s|%t|%s|%s|%s|%s", subjectType, subjectIDValue, roleID, scope.Global, scope.ClusterID, scope.Namespace, scope.Environment, scope.ServiceID)
	sum := sha256.Sum256([]byte(raw))
	return "binding-platform-" + hex.EncodeToString(sum[:])[:16]
}

func roleID(name string, permissions []platformrbac.Permission) string {
	raw := strings.TrimSpace(name)
	for _, permission := range permissions {
		raw += fmt.Sprintf("|%s:%s:%s", permission.Resource, permission.Action, permission.ScopeMode)
	}
	sum := sha256.Sum256([]byte(raw))
	return "role-platform-" + hex.EncodeToString(sum[:])[:16]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}
