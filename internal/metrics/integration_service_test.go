package metrics

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	k8sdeployment "novaapm/internal/modules/k8sops/deployment"
	k8sresource "novaapm/internal/modules/k8sops/resource"
	obsendpoint "novaapm/internal/observability/endpoint"
	platformenvironment "novaapm/internal/platform/environment"
	platformimages "novaapm/internal/platform/images"
	platformrbac "novaapm/internal/platform/rbac"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestCreateIntegrationUsesEnvironmentAsOnlyIdentityAndCreatesSourceAccesses(t *testing.T) {
	fixture := newIntegrationFixture(t)

	created, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{
		EnvironmentID:  fixture.environment.ID,
		DestinationRef: "vm-prod",
	})

	require.NoError(t, err)
	require.Equal(t, fixture.environment.ID, created.Integration.EnvironmentID)
	require.Equal(t, "vm-prod", created.Integration.DestinationRef)
	require.Equal(t, EnvironmentIdentityLabel, created.Integration.IdentityLabelKey)
	require.Len(t, created.SourceAccesses, 2)
	require.Equal(t, SourceKindKubernetesInfra, created.SourceAccesses[0].SourceKind)
	require.Equal(t, SourceKindHostInfra, created.SourceAccesses[1].SourceKind)
	for _, source := range created.SourceAccesses {
		require.Equal(t, CollectionModeExternal, source.CollectionMode)
		require.Equal(t, DesiredStateConnected, source.DesiredState)
	}
}

func TestCreateIntegrationRejectsSecondDestinationForSameEnvironment(t *testing.T) {
	fixture := newIntegrationFixture(t)
	_, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-prod"})
	require.NoError(t, err)

	_, err = fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-other"})

	require.ErrorIs(t, err, ErrIntegrationAlreadyExists)
}

func TestUpdateSourceAccessAllowsManagedK8sAndExternalHostWithinEnvironment(t *testing.T) {
	fixture := newIntegrationFixture(t)
	created, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-prod"})
	require.NoError(t, err)

	updated, err := fixture.service.UpdateSourceAccess(context.Background(), integrationSubject(), created.SourceAccesses[0].ID, UpdateSourceAccessRequest{CollectionMode: CollectionModeManaged})

	require.NoError(t, err)
	require.Equal(t, SourceKindKubernetesInfra, updated.SourceKind)
	require.Equal(t, CollectionModeManaged, updated.CollectionMode)
	require.Equal(t, CollectionModeExternal, created.SourceAccesses[1].CollectionMode)
}

func TestUpdateSourceAccessRejectsManagedHostWithoutExecutionChannel(t *testing.T) {
	fixture := newIntegrationFixture(t)
	created, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-prod"})
	require.NoError(t, err)

	_, err = fixture.service.UpdateSourceAccess(context.Background(), integrationSubject(), created.SourceAccesses[1].ID, UpdateSourceAccessRequest{CollectionMode: CollectionModeManaged})

	require.ErrorContains(t, err, "只有 K8s Infra")
}

func TestCreateIntegrationRejectsArchivedEnvironment(t *testing.T) {
	fixture := newIntegrationFixture(t)
	fixture.environment.Status = platformenvironment.StatusArchived
	require.NoError(t, fixture.environments.UpdateEnvironment(context.Background(), fixture.environment))

	_, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-prod"})

	require.ErrorIs(t, err, ErrEnvironmentUnavailable)
}

func TestIntegrationOperationsRequireEnvironmentScopedPermissions(t *testing.T) {
	fixture := newIntegrationFixture(t)
	fixture.service.authorizer = integrationDenyAuthorizer{}

	_, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-prod"})

	require.ErrorIs(t, err, ErrPermissionDenied)
}

func TestGetSourceHandoffGeneratesOperatorAndVmagentFragments(t *testing.T) {
	fixture := newIntegrationFixture(t)
	created, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-prod"})
	require.NoError(t, err)

	handoff, err := fixture.service.GetSourceHandoff(context.Background(), integrationSubject(), created.SourceAccesses[0].ID)

	require.NoError(t, err)
	require.Equal(t, "cluster-prod", handoff.ResourceRef)
	require.Len(t, handoff.Artifacts, 3)
	require.Equal(t, "vmoperator_patch", handoff.Artifacts[0].Kind)
	require.Contains(t, handoff.Artifacts[0].Content, "novaapm_environment_id: env-prod")
	require.Contains(t, handoff.Artifacts[0].Content, "https://vm.example/api/v1/write")
	require.Contains(t, handoff.Artifacts[1].Content, "-remoteWrite.label=novaapm_environment_id=env-prod")
}

func TestVerifyIntegrationPersistsFourLayerSnapshot(t *testing.T) {
	fixture := newIntegrationFixture(t)
	created, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-prod"})
	require.NoError(t, err)
	fixture.service.verifier = staticHealthVerifier{}

	snapshot, err := fixture.service.VerifyIntegration(context.Background(), integrationSubject(), created.Integration.ID)

	require.NoError(t, err)
	require.Equal(t, HealthHealthy, snapshot.Configuration.Status)
	require.Equal(t, HealthHealthy, snapshot.Destination.Status)
	require.Equal(t, HealthHealthy, snapshot.DataFlow.Status)
	require.Len(t, snapshot.Sources, 2)
	overview, err := fixture.service.ListOverview(context.Background(), integrationSubject())
	require.NoError(t, err)
	require.Equal(t, snapshot.ID, overview[0].LatestSnapshot.ID)
}

func TestVerifyIntegrationMarksArchivedEnvironmentConfigurationFailed(t *testing.T) {
	fixture := newIntegrationFixture(t)
	created, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-prod"})
	require.NoError(t, err)
	fixture.service.verifier = staticHealthVerifier{}
	fixture.environment.Status = platformenvironment.StatusArchived
	require.NoError(t, fixture.environments.UpdateEnvironment(context.Background(), fixture.environment))

	snapshot, err := fixture.service.VerifyIntegration(context.Background(), integrationSubject(), created.Integration.ID)

	require.NoError(t, err)
	require.Equal(t, HealthFailed, snapshot.Configuration.Status)
	require.Contains(t, snapshot.Configuration.Message, "归档")
}

func TestManagedKubernetesCollectorRequiresPreviewBeforeApply(t *testing.T) {
	fixture := newIntegrationFixture(t)
	created, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-prod"})
	require.NoError(t, err)
	source := created.SourceAccesses[0]
	_, err = fixture.service.UpdateSourceAccess(context.Background(), integrationSubject(), source.ID, UpdateSourceAccessRequest{CollectionMode: CollectionModeManaged})
	require.NoError(t, err)
	deployer := &managedDeployerStub{}
	fixture.service.deployer = deployer
	fixture.service.imageTemplates = imageTemplateStub{}

	preview, err := fixture.service.PreviewManagedCollector(context.Background(), integrationSubject(), source.ID, PreviewCollectorReleaseRequest{})
	require.NoError(t, err)
	require.Equal(t, ReleasePreviewed, preview.Status)
	require.Equal(t, "cluster-prod", preview.ClusterID)
	require.NotEmpty(t, preview.ManifestHash)
	applied, err := fixture.service.ApplyManagedCollector(context.Background(), integrationSubject(), source.ID)
	require.NoError(t, err)
	require.Equal(t, ReleaseApplied, applied.Status)
	require.Contains(t, deployer.manifest, "-remoteWrite.label=novaapm_environment_id=env-prod")
	decoder := yaml.NewDecoder(strings.NewReader(deployer.manifest))
	documentCount := 0
	for {
		var document map[string]any
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		documentCount++
	}
	require.Equal(t, 6, documentCount)
}

func TestEnableLogDerivedSourceUsesSameEnvironmentIntegration(t *testing.T) {
	fixture := newIntegrationFixture(t)
	created, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-prod"})
	require.NoError(t, err)

	source, err := fixture.service.EnableLogDerivedSource(context.Background(), integrationSubject(), created.Integration.ID)
	require.NoError(t, err)
	require.Equal(t, SourceKindLogDerived, source.SourceKind)
	handoff, err := fixture.service.GetSourceHandoff(context.Background(), integrationSubject(), source.ID)
	require.NoError(t, err)
	require.Equal(t, "vmalert_args", handoff.Artifacts[0].Kind)
	require.Contains(t, handoff.Artifacts[0].Content, "novaapm_environment_id=env-prod")
}

func TestOverviewBuildsGrafanaDrilldownWithEnvironmentVariable(t *testing.T) {
	fixture := newIntegrationFixture(t)
	created, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-prod", DashboardRef: "grafana-prod"})
	require.NoError(t, err)
	require.Equal(t, "grafana-prod", created.Integration.DashboardRef)

	overview, err := fixture.service.ListOverview(context.Background(), integrationSubject())
	require.NoError(t, err)
	require.Contains(t, overview[0].GrafanaURL, "var-novaapm_environment_id=env-prod")
}

func TestUpdateIntegrationChangesDestinationDashboardAndConnectionState(t *testing.T) {
	fixture := newIntegrationFixture(t)
	created, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-prod"})
	require.NoError(t, err)
	destination, dashboard, state := "vm-other", "grafana-prod", DesiredStateDisconnected

	updated, err := fixture.service.UpdateIntegration(context.Background(), integrationSubject(), created.Integration.ID, UpdateIntegrationRequest{DestinationRef: &destination, DashboardRef: &dashboard, DesiredState: &state})

	require.NoError(t, err)
	require.Equal(t, destination, updated.Integration.DestinationRef)
	require.Equal(t, dashboard, updated.Integration.DashboardRef)
	require.Equal(t, state, updated.Integration.DesiredState)
}

func TestReconcileSourcesFollowsCurrentEnvironmentBindings(t *testing.T) {
	fixture := newIntegrationFixture(t)
	created, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-prod"})
	require.NoError(t, err)
	require.NoError(t, fixture.environments.DeleteResourceBinding(context.Background(), "binding-host"))
	require.NoError(t, fixture.environments.CreateResourceBinding(context.Background(), platformenvironment.ResourceBinding{ID: "binding-k8s-2", EnvironmentID: fixture.environment.ID, ResourceKind: platformenvironment.ResourceKindK8sCluster, ResourceRef: "cluster-dr"}))

	reconciled, err := fixture.service.ReconcileSources(context.Background(), integrationSubject(), created.Integration.ID)

	require.NoError(t, err)
	require.Len(t, reconciled.SourceAccesses, 2)
	require.Equal(t, "binding-k8s", reconciled.SourceAccesses[0].ResourceBindingID)
	require.Equal(t, "binding-k8s-2", reconciled.SourceAccesses[1].ResourceBindingID)
}

type integrationFixture struct {
	service      IntegrationService
	repository   *MemoryIntegrationRepository
	environments *platformenvironment.MemoryRepository
	environment  platformenvironment.Environment
}

func newIntegrationFixture(t *testing.T) integrationFixture {
	t.Helper()
	environments := platformenvironment.NewMemoryRepository()
	environment := platformenvironment.Environment{ID: "env-prod", Name: "生产环境", Stage: platformenvironment.StageProduction, Status: platformenvironment.StatusActive}
	require.NoError(t, environments.CreateEnvironment(context.Background(), environment))
	require.NoError(t, environments.CreateResourceBinding(context.Background(), platformenvironment.ResourceBinding{ID: "binding-k8s", EnvironmentID: environment.ID, ResourceKind: platformenvironment.ResourceKindK8sCluster, ResourceRef: "cluster-prod"}))
	require.NoError(t, environments.CreateResourceBinding(context.Background(), platformenvironment.ResourceBinding{ID: "binding-host", EnvironmentID: environment.ID, ResourceKind: platformenvironment.ResourceKindHostGroup, ResourceRef: "prod-vms"}))
	repository := NewMemoryIntegrationRepository()
	service := NewIntegrationService(IntegrationDependencies{
		Repository:   repository,
		Environments: environments,
		Destinations: staticDestinationReader{ids: map[string]bool{"vm-prod": true, "vm-other": true}},
		Authorizer:   integrationAllowAuthorizer{},
		K8sResources: k8sResourceReaderStub{},
	})
	return integrationFixture{service: service, repository: repository, environments: environments, environment: environment}
}

func TestManagedCollectorPreviewRejectsClusterWithExistingCollector(t *testing.T) {
	fixture := newIntegrationFixture(t)
	created, err := fixture.service.CreateIntegration(context.Background(), integrationSubject(), CreateIntegrationRequest{EnvironmentID: fixture.environment.ID, DestinationRef: "vm-prod"})
	require.NoError(t, err)
	source := created.SourceAccesses[0]
	_, err = fixture.service.UpdateSourceAccess(context.Background(), integrationSubject(), source.ID, UpdateSourceAccessRequest{CollectionMode: CollectionModeManaged})
	require.NoError(t, err)
	fixture.service.deployer = &managedDeployerStub{}
	fixture.service.imageTemplates = imageTemplateStub{}
	fixture.service.k8sResources = k8sResourceReaderStub{resources: []k8sresource.ResourceSummary{{Identity: k8sresource.Identity{Kind: "Deployment", Name: "vmagent-existing"}}}}

	_, err = fixture.service.PreviewManagedCollector(context.Background(), integrationSubject(), source.ID, PreviewCollectorReleaseRequest{})

	require.ErrorIs(t, err, ErrCollectorAlreadyPresent)
}

type staticDestinationReader struct{ ids map[string]bool }

func (r staticDestinationReader) IsMetricsWriteDestination(_ context.Context, id string) (bool, error) {
	return r.ids[id], nil
}

func (r staticDestinationReader) ListOptions(_ context.Context) ([]obsendpoint.Endpoint, error) {
	return []obsendpoint.Endpoint{{ID: "vm-prod", URLs: obsendpoint.EndpointURLs{RemoteWriteURL: "https://vm.example/api/v1/write"}}}, nil
}

func (r staticDestinationReader) GetOption(_ context.Context, id string) (obsendpoint.Endpoint, error) {
	if id == "grafana-prod" {
		return obsendpoint.Endpoint{ID: id, Kind: obsendpoint.KindGrafana, Status: "active", URLs: obsendpoint.EndpointURLs{UIURL: "https://grafana.example/d/k8s"}}, nil
	}
	if !r.ids[id] {
		return obsendpoint.Endpoint{}, ErrDestinationUnavailable
	}
	return obsendpoint.Endpoint{ID: id, URLs: obsendpoint.EndpointURLs{RemoteWriteURL: "https://vm.example/api/v1/write"}}, nil
}

func (r staticDestinationReader) ListDashboardOptions(_ context.Context) ([]obsendpoint.Endpoint, error) {
	return []obsendpoint.Endpoint{{ID: "grafana-prod", Name: "生产 Grafana", Kind: obsendpoint.KindGrafana, Status: "active", URLs: obsendpoint.EndpointURLs{UIURL: "https://grafana.example/d/k8s"}}}, nil
}
func (r staticDestinationReader) IsDashboard(_ context.Context, id string) (bool, error) {
	return id == "grafana-prod", nil
}

type integrationAllowAuthorizer struct{}

func (integrationAllowAuthorizer) Authorize(platformrbac.Subject, platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: true}
}

type integrationDenyAuthorizer struct{}

func (integrationDenyAuthorizer) Authorize(platformrbac.Subject, platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false}
}

type staticHealthVerifier struct{}

func (staticHealthVerifier) Verify(_ context.Context, _ obsendpoint.Endpoint, _ string, sources []SourceAccess, observedAt time.Time) (HealthLayer, HealthLayer, []SourceHealth, []EnvironmentSignal) {
	health := make([]SourceHealth, 0, len(sources))
	for _, source := range sources {
		health = append(health, SourceHealth{SourceAccessID: source.ID, SourceKind: source.SourceKind, Status: HealthHealthy, Message: "覆盖完整"})
	}
	return HealthLayer{Status: HealthHealthy, Message: "目标可查询", ObservedAt: observedAt}, HealthLayer{Status: HealthHealthy, Message: "数据新鲜", ObservedAt: observedAt}, health, []EnvironmentSignal{{Key: "cpu_utilization", Label: "CPU 使用率", Value: .5, Unit: "ratio", Status: HealthHealthy}}
}

type managedDeployerStub struct{ manifest string }

func (s *managedDeployerStub) Preview(_ context.Context, _ platformrbac.Subject, request k8sdeployment.OperationRequest) (k8sdeployment.OperationResult, error) {
	s.manifest = request.YAMLContent
	return k8sdeployment.OperationResult{Status: "previewed", Message: "预览完成", PreviewID: "preview-1", ConfirmationToken: "confirm-1", Resources: []k8sdeployment.ResourceIdentity{{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "novaapm-system", Name: "novaapm-vmagent"}}}, nil
}
func (s *managedDeployerStub) Apply(_ context.Context, _ platformrbac.Subject, request k8sdeployment.OperationRequest) (k8sdeployment.OperationResult, error) {
	s.manifest = request.YAMLContent
	if request.PreviewID != "preview-1" || request.ConfirmationToken != "confirm-1" {
		return k8sdeployment.OperationResult{}, errors.New("确认不匹配")
	}
	return k8sdeployment.OperationResult{Status: "applied", Message: "发布完成"}, nil
}

type imageTemplateStub struct{}

func (imageTemplateStub) TemplateValues(context.Context) (map[string]string, error) {
	return map[string]string{platformimages.VMAgentImagePlaceholder: "registry.example/vmagent:v1"}, nil
}

type k8sResourceReaderStub struct{ resources []k8sresource.ResourceSummary }

func (s k8sResourceReaderStub) List(context.Context, k8sresource.ListFilter) ([]k8sresource.ResourceSummary, error) {
	return append([]k8sresource.ResourceSummary(nil), s.resources...), nil
}

func integrationSubject() platformrbac.Subject {
	return platformrbac.Subject{ID: "operator-1", Type: "user", DisplayName: "指标运维"}
}
