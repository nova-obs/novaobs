package environment

import (
	"context"
	"testing"

	platformaudit "novaapm/internal/platform/audit"
	platformrbac "novaapm/internal/platform/rbac"

	"github.com/stretchr/testify/require"
)

func TestCreateEnvironmentEstablishesStablePlatformIdentity(t *testing.T) {
	repo := NewMemoryRepository()
	service := NewService(repo, allowAllResourceValidator{}, WithAuthorizer(allowAllAuthorizer{}))

	created, err := service.Create(context.Background(), testSubject(), CreateRequest{
		Name: " 生产环境 ", Stage: StageProduction, Description: " 核心生产环境 ",
	})

	require.NoError(t, err)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "生产环境", created.Name)
	require.Equal(t, StageProduction, created.Stage)
	require.Equal(t, StatusActive, created.Status)
	require.Equal(t, "核心生产环境", created.Description)
	require.False(t, created.CreatedAt.IsZero())
	require.Equal(t, created.CreatedAt, created.UpdatedAt)
}

func TestCreateEnvironmentRejectsInvalidStage(t *testing.T) {
	service := NewService(NewMemoryRepository(), allowAllResourceValidator{}, WithAuthorizer(allowAllAuthorizer{}))

	_, err := service.Create(context.Background(), testSubject(), CreateRequest{Name: "生产环境", Stage: "prod"})

	require.Error(t, err)
	require.Contains(t, err.Error(), "stage")
}

func TestEnvironmentOperationsRequirePlatformPermission(t *testing.T) {
	service := NewService(NewMemoryRepository(), allowAllResourceValidator{}, WithAuthorizer(denyAllAuthorizer{}))

	_, err := service.List(context.Background(), testSubject())
	require.ErrorIs(t, err, ErrPermissionDenied)

	_, err = service.Create(context.Background(), testSubject(), CreateRequest{Name: "生产环境", Stage: StageProduction})
	require.ErrorIs(t, err, ErrPermissionDenied)
}

func TestBindResourceEnforcesGlobalResourceOwnership(t *testing.T) {
	repo := NewMemoryRepository()
	service := NewService(repo, allowAllResourceValidator{}, WithAuthorizer(allowAllAuthorizer{}))
	first, err := service.Create(context.Background(), testSubject(), CreateRequest{Name: "生产环境", Stage: StageProduction})
	require.NoError(t, err)
	second, err := service.Create(context.Background(), testSubject(), CreateRequest{Name: "预发环境", Stage: StageStaging})
	require.NoError(t, err)

	binding, err := service.BindResource(context.Background(), testSubject(), first.ID, BindResourceRequest{
		ResourceKind: ResourceKindK8sCluster,
		ResourceRef:  "cluster-prod-1",
	})
	require.NoError(t, err)
	require.Equal(t, first.ID, binding.EnvironmentID)

	_, err = service.BindResource(context.Background(), testSubject(), second.ID, BindResourceRequest{
		ResourceKind: ResourceKindK8sCluster,
		ResourceRef:  "cluster-prod-1",
	})
	require.ErrorIs(t, err, ErrResourceAlreadyBound)
}

func TestBindResourceRejectsUnknownResourceKind(t *testing.T) {
	repo := NewMemoryRepository()
	service := NewService(repo, allowAllResourceValidator{}, WithAuthorizer(allowAllAuthorizer{}))
	item, err := service.Create(context.Background(), testSubject(), CreateRequest{Name: "生产环境", Stage: StageProduction})
	require.NoError(t, err)

	_, err = service.BindResource(context.Background(), testSubject(), item.ID, BindResourceRequest{
		ResourceKind: "cloud_region",
		ResourceRef:  "cn-bj2",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "resource_kind")
}

func TestArchiveEnvironmentKeepsIdentityAndPreventsNewBindings(t *testing.T) {
	repo := NewMemoryRepository()
	service := NewService(repo, allowAllResourceValidator{}, WithAuthorizer(allowAllAuthorizer{}))
	item, err := service.Create(context.Background(), testSubject(), CreateRequest{Name: "临时环境", Stage: StageTest})
	require.NoError(t, err)

	archived, err := service.Update(context.Background(), testSubject(), item.ID, UpdateRequest{Status: stringPointer(StatusArchived)})
	require.NoError(t, err)
	require.Equal(t, item.ID, archived.ID)
	require.Equal(t, StatusArchived, archived.Status)

	_, err = service.BindResource(context.Background(), testSubject(), item.ID, BindResourceRequest{
		ResourceKind: ResourceKindHostGroup,
		ResourceRef:  "test-vms",
	})
	require.ErrorIs(t, err, ErrEnvironmentArchived)
}

func TestGetEnvironmentReturnsBindingsWithoutDuplicatingResourceTruth(t *testing.T) {
	repo := NewMemoryRepository()
	service := NewService(repo, allowAllResourceValidator{}, WithAuthorizer(allowAllAuthorizer{}))
	item, err := service.Create(context.Background(), testSubject(), CreateRequest{Name: "混合环境", Stage: StageProduction})
	require.NoError(t, err)
	_, err = service.BindResource(context.Background(), testSubject(), item.ID, BindResourceRequest{ResourceKind: ResourceKindK8sCluster, ResourceRef: "cluster-1"})
	require.NoError(t, err)
	_, err = service.BindResource(context.Background(), testSubject(), item.ID, BindResourceRequest{ResourceKind: ResourceKindHostGroup, ResourceRef: "vm-prod"})
	require.NoError(t, err)

	view, err := service.Get(context.Background(), testSubject(), item.ID)

	require.NoError(t, err)
	require.Equal(t, item.ID, view.Environment.ID)
	require.Len(t, view.ResourceBindings, 2)
	require.Equal(t, ResourceKindK8sCluster, view.ResourceBindings[0].ResourceKind)
	require.Equal(t, ResourceKindHostGroup, view.ResourceBindings[1].ResourceKind)
}

func TestEnvironmentWritesProducePlatformAuditEvents(t *testing.T) {
	repo := NewMemoryRepository()
	auditor := &recordingAuditor{}
	service := NewService(repo, allowAllResourceValidator{}, WithAuthorizer(allowAllAuthorizer{}), WithAuditor(auditor))

	item, err := service.Create(context.Background(), testSubject(), CreateRequest{Name: "生产环境", Stage: StageProduction})
	require.NoError(t, err)
	_, err = service.BindResource(context.Background(), testSubject(), item.ID, BindResourceRequest{ResourceKind: ResourceKindK8sCluster, ResourceRef: "cluster-prod"})
	require.NoError(t, err)

	require.Len(t, auditor.events, 2)
	require.Equal(t, "environment", auditor.events[0].Resource.Type)
	require.Equal(t, "create", auditor.events[0].Action)
	require.Equal(t, "environment_resource_binding", auditor.events[1].Resource.Type)
	require.Equal(t, testSubject().ID, auditor.events[1].Actor.ID)
}

type allowAllAuthorizer struct{}

type allowAllResourceValidator struct{}

func (allowAllResourceValidator) Validate(context.Context, string, string) error { return nil }

func (allowAllAuthorizer) Authorize(platformrbac.Subject, platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: true}
}

type denyAllAuthorizer struct{}

func (denyAllAuthorizer) Authorize(platformrbac.Subject, platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false}
}

func testSubject() platformrbac.Subject {
	return platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "测试用户"}
}

func stringPointer(value string) *string { return &value }

type recordingAuditor struct {
	events []platformaudit.Event
}

func (a *recordingAuditor) Record(_ context.Context, event platformaudit.Event) (platformaudit.Event, error) {
	a.events = append(a.events, event)
	return event, nil
}
