package namespace

import (
	"context"
	"errors"
	"testing"

	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/platform/audit"
	platformrbac "novaobs/internal/platform/rbac"

	"github.com/stretchr/testify/require"
)

func TestServiceListsNamespaces(t *testing.T) {
	repo := NewMemoryRepository([]Namespace{{ID: "orders", Name: "orders", ClusterID: "prod"}})
	svc := NewService(repo)

	items, err := svc.List(context.Background(), ListFilter{ClusterID: "prod"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "orders", items[0].Name)
}

func TestMemoryRepositoryFiltersSortsAndPaginatesNamespaces(t *testing.T) {
	repo := NewMemoryRepository([]Namespace{
		{ID: "orders", Name: "orders", ClusterID: "prod", Status: "active"},
		{ID: "payment", Name: "payment", ClusterID: "prod", Status: "active"},
		{ID: "sandbox", Name: "sandbox", ClusterID: "dev", Status: "paused"},
	})
	svc := NewService(repo)

	items, err := svc.List(context.Background(), ListFilter{
		ClusterID: "prod",
		Sort:      "name",
		Order:     "desc",
		Page:      2,
		PageSize:  1,
	})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "orders", items[0].Name)
}

func TestServiceCreatesNamespaceWithPermissionAndAudit(t *testing.T) {
	repo := NewMemoryRepository(nil)
	auditStore := audit.NewMemoryStore()
	service := NewService(repo, platformrbac.NewService(namespaceWriterRepo()), audit.NewService(auditStore))

	created, event, err := service.Create(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"}, CreateRequest{
		ClusterID: "prod",
		Name:      "orders",
		Owner:     "orders-team",
	})

	require.NoError(t, err)
	require.Equal(t, "orders", created.Name)
	require.Equal(t, "orders-team", created.Owner)
	require.NotEmpty(t, event.ID)
	events, listErr := auditStore.List(context.Background())
	require.NoError(t, listErr)
	require.Len(t, events, 1)
	require.Equal(t, "k8s.namespace", events[0].ResourceType)
	require.Equal(t, "create", events[0].Action)
	require.Equal(t, "success", events[0].Result)
}

func TestServiceCreateNamespaceRequiresPermission(t *testing.T) {
	service := NewService(NewMemoryRepository(nil), platformrbac.NewService(namespaceEmptyRBACRepo{}), audit.NewService(audit.NewMemoryStore()))

	_, _, err := service.Create(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, CreateRequest{ClusterID: "prod", Name: "orders"})

	require.ErrorIs(t, err, ErrPermissionDenied)
}

func TestServiceCreateNamespaceBlocksReadOnlyCluster(t *testing.T) {
	repo := NewMemoryRepository(nil)
	service := NewService(repo, platformrbac.NewService(namespaceWriterRepo()), audit.NewService(audit.NewMemoryStore()), namespaceStaticClusterPolicy{readOnly: true})

	_, _, err := service.Create(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, CreateRequest{ClusterID: "prod", Name: "orders"})

	require.ErrorIs(t, err, cluster.ErrClusterReadOnly)
	items, listErr := repo.List(context.Background(), ListFilter{ClusterID: "prod"})
	require.NoError(t, listErr)
	require.Empty(t, items)
}

func TestServiceCreateNamespaceRollsBackWhenAuditFails(t *testing.T) {
	repo := NewMemoryRepository(nil)
	service := NewService(repo, platformrbac.NewService(namespaceWriterRepo()), namespaceFailingAuditor{})

	_, _, err := service.Create(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, CreateRequest{ClusterID: "prod", Name: "orders"})

	require.Error(t, err)
	items, listErr := repo.List(context.Background(), ListFilter{ClusterID: "prod"})
	require.NoError(t, listErr)
	require.Empty(t, items)
}

func TestServiceDeletesNamespaceWithPermissionAndAudit(t *testing.T) {
	repo := NewMemoryRepository([]Namespace{{ID: "uid-orders", ClusterID: "prod", Name: "orders", Status: "active"}})
	auditStore := audit.NewMemoryStore()
	service := NewService(repo, platformrbac.NewService(namespaceWriterRepo()), audit.NewService(auditStore))

	event, err := service.Delete(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, DeleteRequest{
		ClusterID: "prod",
		Name:      "orders",
		UID:       "uid-orders",
	})

	require.NoError(t, err)
	require.NotEmpty(t, event.ID)
	items, listErr := repo.List(context.Background(), ListFilter{ClusterID: "prod"})
	require.NoError(t, listErr)
	require.Empty(t, items)
	events, auditErr := auditStore.List(context.Background())
	require.NoError(t, auditErr)
	require.Equal(t, "delete", events[0].Action)
}

func TestServiceDeleteNamespaceRollsBackWhenAuditFails(t *testing.T) {
	repo := NewMemoryRepository([]Namespace{{ID: "uid-orders", ClusterID: "prod", Name: "orders", Status: "active"}})
	service := NewService(repo, platformrbac.NewService(namespaceWriterRepo()), namespaceFailingAuditor{})

	_, err := service.Delete(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, DeleteRequest{
		ClusterID: "prod",
		Name:      "orders",
		UID:       "uid-orders",
	})

	require.Error(t, err)
	items, listErr := repo.List(context.Background(), ListFilter{ClusterID: "prod"})
	require.NoError(t, listErr)
	require.Len(t, items, 1)
	require.Equal(t, "orders", items[0].Name)
}

type namespaceFailingAuditor struct{}

func (namespaceFailingAuditor) Record(context.Context, audit.Event) (audit.Event, error) {
	return audit.Event{}, errors.New("audit failed")
}

type namespaceStaticClusterPolicy struct {
	readOnly bool
}

func (p namespaceStaticClusterPolicy) IsReadOnly(context.Context, string) (bool, error) {
	return p.readOnly, nil
}

func namespaceWriterRepo() namespaceRBACRepo {
	return namespaceRBACRepo{
		roles: map[string]platformrbac.Role{
			"role-namespace-writer": {
				ID: "role-namespace-writer",
				Permissions: []platformrbac.Permission{
					{Resource: "k8s.namespace", Action: "create", ScopeMode: "cluster"},
					{Resource: "k8s.namespace", Action: "delete", ScopeMode: "cluster"},
				},
			},
		},
		bindings: []platformrbac.Binding{
			{ID: "binding-1", SubjectID: "user-1", SubjectType: "user", RoleID: "role-namespace-writer", Scope: platformrbac.Scope{ClusterID: "prod"}},
		},
	}
}

type namespaceEmptyRBACRepo struct{}

func (namespaceEmptyRBACRepo) SaveRole(platformrbac.Role) error {
	return nil
}

func (namespaceEmptyRBACRepo) GetRole(string) (platformrbac.Role, error) {
	return platformrbac.Role{}, errors.New("role not found")
}

func (namespaceEmptyRBACRepo) SaveBinding(platformrbac.Binding) error {
	return nil
}

func (namespaceEmptyRBACRepo) ListBindingsBySubject(string, string) ([]platformrbac.Binding, error) {
	return nil, nil
}

type namespaceRBACRepo struct {
	roles    map[string]platformrbac.Role
	bindings []platformrbac.Binding
}

func (r namespaceRBACRepo) SaveRole(platformrbac.Role) error {
	return nil
}

func (r namespaceRBACRepo) GetRole(id string) (platformrbac.Role, error) {
	role, ok := r.roles[id]
	if !ok {
		return platformrbac.Role{}, errors.New("role not found")
	}
	return role, nil
}

func (r namespaceRBACRepo) SaveBinding(platformrbac.Binding) error {
	return nil
}

func (r namespaceRBACRepo) ListBindingsBySubject(subjectID string, subjectType string) ([]platformrbac.Binding, error) {
	out := make([]platformrbac.Binding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		if binding.SubjectID == subjectID && binding.SubjectType == subjectType {
			out = append(out, binding)
		}
	}
	return out, nil
}
