package metrics

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	k8sresource "novaapm/internal/modules/k8sops/resource"
	obsendpoint "novaapm/internal/observability/endpoint"
	platformenvironment "novaapm/internal/platform/environment"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/pkg/apperr"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var (
	ErrPermissionDenied       = errors.New("permission_denied")
	ErrEnvironmentUnavailable = errors.New("metrics_environment_unavailable")
	ErrDestinationUnavailable = errors.New("metrics_destination_unavailable")
)

type Authorizer interface {
	Authorize(subject platformrbac.Subject, request platformrbac.Request) platformrbac.Decision
}

type KubernetesResourceReader interface {
	List(ctx context.Context, filter k8sresource.ListFilter) ([]k8sresource.ResourceSummary, error)
}

type EnvironmentRepository interface {
	GetEnvironment(ctx context.Context, id string) (platformenvironment.Environment, error)
	ListResourceBindings(ctx context.Context, environmentID string) ([]platformenvironment.ResourceBinding, error)
}

type MetricsDestinationReader interface {
	IsMetricsWriteDestination(ctx context.Context, id string) (bool, error)
}

type MetricsDestinationOptionsReader interface {
	ListOptions(ctx context.Context) ([]obsendpoint.Endpoint, error)
	GetOption(ctx context.Context, id string) (obsendpoint.Endpoint, error)
	ListDashboardOptions(ctx context.Context) ([]obsendpoint.Endpoint, error)
	IsDashboard(ctx context.Context, id string) (bool, error)
}

func (s IntegrationService) GetSourceHandoff(ctx context.Context, subject platformrbac.Subject, id string) (SourceHandoff, error) {
	source, err := s.repository.GetSourceAccess(ctx, strings.TrimSpace(id))
	if err != nil {
		return SourceHandoff{}, err
	}
	integration, err := s.repository.GetIntegration(ctx, source.IntegrationID)
	if err != nil {
		return SourceHandoff{}, err
	}
	if !s.allowed(subject, integration.EnvironmentID, "metrics.integration", "read") {
		return SourceHandoff{}, ErrPermissionDenied
	}
	reader, ok := s.destinations.(MetricsDestinationOptionsReader)
	if !ok {
		return SourceHandoff{}, ErrDestinationUnavailable
	}
	destination, err := reader.GetOption(ctx, integration.DestinationRef)
	if err != nil || strings.TrimSpace(destination.URLs.RemoteWriteURL) == "" {
		return SourceHandoff{}, ErrDestinationUnavailable
	}
	bindings, err := s.environments.ListResourceBindings(ctx, integration.EnvironmentID)
	if err != nil {
		return SourceHandoff{}, err
	}
	resourceRef := ""
	for _, binding := range bindings {
		if binding.ID == source.ResourceBindingID {
			resourceRef = binding.ResourceRef
			break
		}
	}
	if resourceRef == "" && source.SourceKind != SourceKindLogDerived {
		return SourceHandoff{}, ErrEnvironmentUnavailable
	}
	return SourceHandoff{
		SourceAccessID: source.ID,
		EnvironmentID:  integration.EnvironmentID,
		ResourceRef:    resourceRef,
		DestinationRef: integration.DestinationRef,
		Artifacts:      buildHandoffArtifacts(source.SourceKind, integration.EnvironmentID, destination.URLs.RemoteWriteURL),
	}, nil
}

type IntegrationDependencies struct {
	Repository     IntegrationRepository
	Environments   EnvironmentRepository
	Destinations   MetricsDestinationReader
	Authorizer     Authorizer
	Verifier       HealthVerifier
	Deployer       ManagedCollectorDeployer
	ImageTemplates ImageTemplateReader
	K8sResources   KubernetesResourceReader
}

type IntegrationService struct {
	repository     IntegrationRepository
	environments   EnvironmentRepository
	destinations   MetricsDestinationReader
	authorizer     Authorizer
	verifier       HealthVerifier
	deployer       ManagedCollectorDeployer
	imageTemplates ImageTemplateReader
	k8sResources   KubernetesResourceReader
	now            func() time.Time
}

func NewIntegrationService(deps IntegrationDependencies) IntegrationService {
	verifier := deps.Verifier
	if verifier == nil {
		verifier = NewVictoriaMetricsHealthVerifier(nil)
	}
	return IntegrationService{repository: deps.Repository, environments: deps.Environments, destinations: deps.Destinations, authorizer: deps.Authorizer, verifier: verifier, deployer: deps.Deployer, imageTemplates: deps.ImageTemplates, k8sResources: deps.K8sResources, now: func() time.Time { return time.Now().UTC() }}
}

func (s IntegrationService) ListOverview(ctx context.Context, subject platformrbac.Subject) ([]OverviewItem, error) {
	views, err := s.ListIntegrations(ctx, subject)
	if err != nil {
		return nil, err
	}
	items := make([]OverviewItem, 0, len(views))
	for _, view := range views {
		item := OverviewItem{Integration: view.Integration, SourceAccesses: view.SourceAccesses}
		if snapshot, err := s.repository.GetLatestHealthSnapshot(ctx, view.Integration.ID); err == nil {
			item.LatestSnapshot = &snapshot
		}
		if view.Integration.DashboardRef != "" {
			item.GrafanaURL = s.grafanaURL(ctx, view.Integration.DashboardRef, view.Integration.EnvironmentID)
		}
		items = append(items, item)
	}
	return items, nil
}

func (s IntegrationService) VerifyIntegration(ctx context.Context, subject platformrbac.Subject, id string) (HealthSnapshot, error) {
	integration, err := s.repository.GetIntegration(ctx, strings.TrimSpace(id))
	if err != nil {
		return HealthSnapshot{}, err
	}
	if !s.allowed(subject, integration.EnvironmentID, "metrics.integration", "manage") {
		return HealthSnapshot{}, ErrPermissionDenied
	}
	reader, ok := s.destinations.(MetricsDestinationOptionsReader)
	if !ok {
		return HealthSnapshot{}, ErrDestinationUnavailable
	}
	destination, err := reader.GetOption(ctx, integration.DestinationRef)
	if err != nil {
		return HealthSnapshot{}, ErrDestinationUnavailable
	}
	sources, err := s.repository.ListSourceAccesses(ctx, integration.ID)
	if err != nil {
		return HealthSnapshot{}, err
	}
	now := s.now()
	destinationHealth, dataFlow, sourceHealth, signals := s.verifier.Verify(ctx, destination, integration.EnvironmentID, sources, now)
	snapshot := HealthSnapshot{
		ID: primitive.NewObjectID().Hex(), IntegrationID: integration.ID, EnvironmentID: integration.EnvironmentID,
		Configuration: HealthLayer{Status: HealthHealthy, Message: "环境、写入目标与来源配置完整", ObservedAt: now},
		Destination:   destinationHealth, DataFlow: dataFlow, Sources: sourceHealth, Signals: signals, CreatedAt: now,
	}
	environment, environmentErr := s.environments.GetEnvironment(ctx, integration.EnvironmentID)
	if environmentErr != nil || environment.Status != platformenvironment.StatusActive {
		snapshot.Configuration = HealthLayer{Status: HealthFailed, Message: "环境已归档或不可用", ObservedAt: now}
	} else if integration.DesiredState != DesiredStateConnected {
		snapshot.Configuration = HealthLayer{Status: HealthFailed, Message: "环境指标接入已断开", ObservedAt: now}
	}
	if err := s.repository.SaveHealthSnapshot(ctx, snapshot); err != nil {
		return HealthSnapshot{}, err
	}
	return snapshot, nil
}

func (s IntegrationService) CreateIntegration(ctx context.Context, subject platformrbac.Subject, request CreateIntegrationRequest) (IntegrationView, error) {
	environmentID := strings.TrimSpace(request.EnvironmentID)
	destinationRef := strings.TrimSpace(request.DestinationRef)
	dashboardRef := strings.TrimSpace(request.DashboardRef)
	if environmentID == "" || destinationRef == "" {
		return IntegrationView{}, apperr.InvalidRequest("environment_id 和 destination_ref 不能为空")
	}
	if !s.allowed(subject, environmentID, "metrics.integration", "manage") {
		return IntegrationView{}, ErrPermissionDenied
	}
	environment, err := s.environments.GetEnvironment(ctx, environmentID)
	if err != nil || environment.Status != platformenvironment.StatusActive {
		return IntegrationView{}, ErrEnvironmentUnavailable
	}
	available, err := s.destinations.IsMetricsWriteDestination(ctx, destinationRef)
	if err != nil {
		return IntegrationView{}, err
	}
	if !available {
		return IntegrationView{}, ErrDestinationUnavailable
	}
	if dashboardRef != "" {
		reader, ok := s.destinations.(MetricsDestinationOptionsReader)
		if !ok {
			return IntegrationView{}, ErrDestinationUnavailable
		}
		available, err := reader.IsDashboard(ctx, dashboardRef)
		if err != nil || !available {
			return IntegrationView{}, ErrDestinationUnavailable
		}
	}
	if _, err := s.repository.FindIntegrationByEnvironment(ctx, environmentID); err == nil {
		return IntegrationView{}, ErrIntegrationAlreadyExists
	} else if !errors.Is(err, ErrIntegrationNotFound) {
		return IntegrationView{}, err
	}
	now := s.now()
	integration := Integration{ID: primitive.NewObjectID().Hex(), EnvironmentID: environmentID, DestinationRef: destinationRef, DashboardRef: dashboardRef, DesiredState: DesiredStateConnected, IdentityLabelKey: EnvironmentIdentityLabel, CreatedBy: subject.ID, UpdatedBy: subject.ID, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CreateIntegration(ctx, integration); err != nil {
		return IntegrationView{}, err
	}
	bindings, err := s.environments.ListResourceBindings(ctx, environmentID)
	if err != nil {
		_ = s.repository.DeleteIntegration(ctx, integration.ID)
		return IntegrationView{}, err
	}
	sources := make([]SourceAccess, 0, len(bindings))
	for _, binding := range bindings {
		sourceKind := sourceKindForResource(binding.ResourceKind)
		if sourceKind == "" {
			continue
		}
		source := SourceAccess{ID: primitive.NewObjectID().Hex(), IntegrationID: integration.ID, ResourceBindingID: binding.ID, SourceKind: sourceKind, CollectionMode: CollectionModeExternal, DesiredState: DesiredStateConnected, CreatedBy: subject.ID, UpdatedBy: subject.ID, CreatedAt: now, UpdatedAt: now}
		if err := s.repository.CreateSourceAccess(ctx, source); err != nil {
			_ = s.repository.DeleteIntegration(ctx, integration.ID)
			return IntegrationView{}, err
		}
		sources = append(sources, source)
	}
	sortSourceAccesses(sources)
	return IntegrationView{Integration: integration, SourceAccesses: sources}, nil
}

func (s IntegrationService) ListDashboardOptions(ctx context.Context, subject platformrbac.Subject, environmentID string) ([]DashboardOption, error) {
	environmentID = strings.TrimSpace(environmentID)
	if !s.allowed(subject, environmentID, "metrics.integration", "read") {
		return nil, ErrPermissionDenied
	}
	reader, ok := s.destinations.(MetricsDestinationOptionsReader)
	if !ok {
		return []DashboardOption{}, nil
	}
	items, err := reader.ListDashboardOptions(ctx)
	if err != nil {
		return nil, err
	}
	options := make([]DashboardOption, 0, len(items))
	for _, item := range items {
		options = append(options, DashboardOption{ID: item.ID, Name: item.Name, UIURL: firstNonEmptyMetric(item.URLs.UIURL, item.URLs.BaseURL)})
	}
	return options, nil
}

func (s IntegrationService) grafanaURL(ctx context.Context, dashboardRef string, environmentID string) string {
	reader, ok := s.destinations.(MetricsDestinationOptionsReader)
	if !ok {
		return ""
	}
	endpoint, err := reader.GetOption(ctx, dashboardRef)
	if err != nil {
		return ""
	}
	raw := firstNonEmptyMetric(endpoint.URLs.UIURL, endpoint.URLs.BaseURL)
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	values := parsed.Query()
	values.Set("var-"+EnvironmentIdentityLabel, environmentID)
	parsed.RawQuery = values.Encode()
	return parsed.String()
}

func firstNonEmptyMetric(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s IntegrationService) ListIntegrations(ctx context.Context, subject platformrbac.Subject) ([]IntegrationView, error) {
	items, err := s.repository.ListIntegrations(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]IntegrationView, 0, len(items))
	for _, item := range items {
		if !s.allowed(subject, item.EnvironmentID, "metrics.integration", "read") {
			continue
		}
		view, err := s.integrationView(ctx, item)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (s IntegrationService) ListWriteDestinationOptions(ctx context.Context, subject platformrbac.Subject, environmentID string) ([]WriteDestinationOption, error) {
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return nil, apperr.InvalidRequest("environment_id 不能为空")
	}
	if !s.allowed(subject, environmentID, "metrics.integration", "read") {
		return nil, ErrPermissionDenied
	}
	reader, ok := s.destinations.(MetricsDestinationOptionsReader)
	if !ok {
		return []WriteDestinationOption{}, nil
	}
	items, err := reader.ListOptions(ctx)
	if err != nil {
		return nil, err
	}
	options := make([]WriteDestinationOption, 0, len(items))
	for _, item := range items {
		options = append(options, WriteDestinationOption{ID: item.ID, Name: item.Name, RemoteWriteURL: item.URLs.RemoteWriteURL, QueryURL: item.URLs.QueryURL, UIURL: item.URLs.UIURL})
	}
	return options, nil
}

func (s IntegrationService) GetIntegration(ctx context.Context, subject platformrbac.Subject, id string) (IntegrationView, error) {
	item, err := s.repository.GetIntegration(ctx, strings.TrimSpace(id))
	if err != nil {
		return IntegrationView{}, err
	}
	if !s.allowed(subject, item.EnvironmentID, "metrics.integration", "read") {
		return IntegrationView{}, ErrPermissionDenied
	}
	return s.integrationView(ctx, item)
}

func (s IntegrationService) UpdateIntegration(ctx context.Context, subject platformrbac.Subject, id string, request UpdateIntegrationRequest) (IntegrationView, error) {
	item, err := s.repository.GetIntegration(ctx, strings.TrimSpace(id))
	if err != nil {
		return IntegrationView{}, err
	}
	if !s.allowed(subject, item.EnvironmentID, "metrics.integration", "manage") {
		return IntegrationView{}, ErrPermissionDenied
	}
	if request.DestinationRef != nil {
		destinationRef := strings.TrimSpace(*request.DestinationRef)
		if destinationRef == "" {
			return IntegrationView{}, apperr.InvalidRequest("destination_ref 不能为空")
		}
		available, err := s.destinations.IsMetricsWriteDestination(ctx, destinationRef)
		if err != nil {
			return IntegrationView{}, err
		}
		if !available {
			return IntegrationView{}, ErrDestinationUnavailable
		}
		item.DestinationRef = destinationRef
	}
	if request.DashboardRef != nil {
		dashboardRef := strings.TrimSpace(*request.DashboardRef)
		if dashboardRef != "" {
			reader, ok := s.destinations.(MetricsDestinationOptionsReader)
			if !ok {
				return IntegrationView{}, ErrDestinationUnavailable
			}
			available, err := reader.IsDashboard(ctx, dashboardRef)
			if err != nil || !available {
				return IntegrationView{}, ErrDestinationUnavailable
			}
		}
		item.DashboardRef = dashboardRef
	}
	if request.DesiredState != nil {
		state := strings.TrimSpace(*request.DesiredState)
		if state != DesiredStateConnected && state != DesiredStateDisconnected {
			return IntegrationView{}, apperr.InvalidRequest("desired_state 无效")
		}
		if state == DesiredStateConnected {
			environment, err := s.environments.GetEnvironment(ctx, item.EnvironmentID)
			if err != nil || environment.Status != platformenvironment.StatusActive {
				return IntegrationView{}, ErrEnvironmentUnavailable
			}
		}
		item.DesiredState = state
	}
	item.UpdatedAt = s.now()
	item.UpdatedBy = subject.ID
	if err := s.repository.UpdateIntegration(ctx, item); err != nil {
		return IntegrationView{}, err
	}
	return s.integrationView(ctx, item)
}

// ReconcileSources 让环境资源绑定保持唯一真值：新增绑定创建来源，解绑资源删除对应来源。
func (s IntegrationService) ReconcileSources(ctx context.Context, subject platformrbac.Subject, id string) (IntegrationView, error) {
	item, err := s.repository.GetIntegration(ctx, strings.TrimSpace(id))
	if err != nil {
		return IntegrationView{}, err
	}
	if !s.allowed(subject, item.EnvironmentID, "metrics.integration", "manage") {
		return IntegrationView{}, ErrPermissionDenied
	}
	environment, err := s.environments.GetEnvironment(ctx, item.EnvironmentID)
	if err != nil || environment.Status != platformenvironment.StatusActive {
		return IntegrationView{}, ErrEnvironmentUnavailable
	}
	bindings, err := s.environments.ListResourceBindings(ctx, item.EnvironmentID)
	if err != nil {
		return IntegrationView{}, err
	}
	sources, err := s.repository.ListSourceAccesses(ctx, item.ID)
	if err != nil {
		return IntegrationView{}, err
	}
	bindingByID := make(map[string]platformenvironment.ResourceBinding, len(bindings))
	for _, binding := range bindings {
		bindingByID[binding.ID] = binding
	}
	sourceByBinding := make(map[string]SourceAccess, len(sources))
	for _, source := range sources {
		if source.ResourceBindingID != "" {
			sourceByBinding[source.ResourceBindingID] = source
		}
	}
	for _, source := range sources {
		if source.SourceKind == SourceKindLogDerived {
			continue
		}
		if _, exists := bindingByID[source.ResourceBindingID]; !exists {
			if err := s.repository.DeleteSourceAccess(ctx, source.ID); err != nil {
				return IntegrationView{}, err
			}
		}
	}
	now := s.now()
	for _, binding := range bindings {
		if _, exists := sourceByBinding[binding.ID]; exists {
			continue
		}
		sourceKind := sourceKindForResource(binding.ResourceKind)
		if sourceKind == "" {
			continue
		}
		source := SourceAccess{ID: primitive.NewObjectID().Hex(), IntegrationID: item.ID, ResourceBindingID: binding.ID, SourceKind: sourceKind, CollectionMode: CollectionModeExternal, DesiredState: DesiredStateConnected, CreatedBy: subject.ID, UpdatedBy: subject.ID, CreatedAt: now, UpdatedAt: now}
		if err := s.repository.CreateSourceAccess(ctx, source); err != nil {
			return IntegrationView{}, err
		}
	}
	return s.integrationView(ctx, item)
}

func (s IntegrationService) integrationView(ctx context.Context, item Integration) (IntegrationView, error) {
	sources, err := s.repository.ListSourceAccesses(ctx, item.ID)
	if err != nil {
		return IntegrationView{}, err
	}
	releases := make([]CollectorRelease, 0, len(sources))
	for _, source := range sources {
		release, err := s.repository.GetLatestCollectorRelease(ctx, source.ID)
		if errors.Is(err, ErrIntegrationNotFound) {
			continue
		}
		if err != nil {
			return IntegrationView{}, err
		}
		releases = append(releases, release)
	}
	return IntegrationView{Integration: item, SourceAccesses: sources, CollectorReleases: releases}, nil
}

func (s IntegrationService) UpdateSourceAccess(ctx context.Context, subject platformrbac.Subject, id string, request UpdateSourceAccessRequest) (SourceAccess, error) {
	item, err := s.repository.GetSourceAccess(ctx, strings.TrimSpace(id))
	if err != nil {
		return SourceAccess{}, err
	}
	integration, err := s.repository.GetIntegration(ctx, item.IntegrationID)
	if err != nil {
		return SourceAccess{}, err
	}
	if !s.allowed(subject, integration.EnvironmentID, "metrics.integration", "manage") {
		return SourceAccess{}, ErrPermissionDenied
	}
	mode := strings.TrimSpace(request.CollectionMode)
	if mode != "" {
		if mode != CollectionModeExternal && mode != CollectionModeManaged {
			return SourceAccess{}, apperr.InvalidRequest("collection_mode 无效")
		}
		if mode == CollectionModeManaged && item.SourceKind != SourceKindKubernetesInfra {
			return SourceAccess{}, apperr.InvalidRequest("只有 K8s Infra 来源支持平台受管采集器")
		}
		item.CollectionMode = mode
	}
	state := strings.TrimSpace(request.DesiredState)
	if state != "" {
		if state != DesiredStateConnected && state != DesiredStateDisconnected {
			return SourceAccess{}, apperr.InvalidRequest("desired_state 无效")
		}
		item.DesiredState = state
	}
	item.UpdatedAt = s.now()
	item.UpdatedBy = subject.ID
	if err := s.repository.UpdateSourceAccess(ctx, item); err != nil {
		return SourceAccess{}, err
	}
	return item, nil
}

func (s IntegrationService) EnableLogDerivedSource(ctx context.Context, subject platformrbac.Subject, integrationID string) (SourceAccess, error) {
	integration, err := s.repository.GetIntegration(ctx, strings.TrimSpace(integrationID))
	if err != nil {
		return SourceAccess{}, err
	}
	if !s.allowed(subject, integration.EnvironmentID, "metrics.integration", "manage") {
		return SourceAccess{}, ErrPermissionDenied
	}
	sources, err := s.repository.ListSourceAccesses(ctx, integration.ID)
	if err != nil {
		return SourceAccess{}, err
	}
	for _, source := range sources {
		if source.SourceKind == SourceKindLogDerived {
			return source, nil
		}
	}
	now := s.now()
	source := SourceAccess{ID: primitive.NewObjectID().Hex(), IntegrationID: integration.ID, SourceKind: SourceKindLogDerived, CollectionMode: CollectionModeExternal, DesiredState: DesiredStateConnected, CreatedBy: subject.ID, UpdatedBy: subject.ID, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CreateSourceAccess(ctx, source); err != nil {
		return SourceAccess{}, err
	}
	return source, nil
}

func (s IntegrationService) allowed(subject platformrbac.Subject, environmentID string, resource string, action string) bool {
	if s.authorizer == nil || subject.ID == "" || subject.Type == "" {
		return false
	}
	return s.authorizer.Authorize(subject, platformrbac.Request{Resource: resource, Action: action, Scope: platformrbac.Scope{EnvironmentID: environmentID}}).Allowed
}

func sourceKindForResource(kind string) string {
	switch kind {
	case platformenvironment.ResourceKindK8sCluster:
		return SourceKindKubernetesInfra
	case platformenvironment.ResourceKindHostGroup:
		return SourceKindHostInfra
	default:
		return ""
	}
}
