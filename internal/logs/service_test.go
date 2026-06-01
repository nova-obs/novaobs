package logs

import (
	"context"
	"strings"
	"testing"

	"novaobs/internal/collectormanagement"
	"novaobs/internal/database/memstore"
	k8sopscluster "novaobs/internal/modules/k8sops/cluster"
	k8sopsdeployment "novaobs/internal/modules/k8sops/deployment"
	k8sopsresource "novaobs/internal/modules/k8sops/resource"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/internal/servicecatalog"

	"github.com/stretchr/testify/require"
)

func TestSyncK8sNamespaceServicesCreatesServiceAndTarget(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))

	result, err := fixture.service.SyncK8sNamespaceServices(ctx, SyncK8sNamespaceRequest{
		ClusterID:    "test03",
		Namespace:    "logplatform",
		Environment:  "prod",
		OwnerTeam:    "sre",
		WorkloadKind: "Deployment",
	})

	require.NoError(t, err)
	require.Len(t, result.Services, 2)
	utrace := result.Services[1]
	require.True(t, utrace.Created)
	require.Equal(t, "utrace-api", utrace.Service.Name)
	require.Equal(t, "k8s_workload", utrace.Service.IdentityType)
	require.Equal(t, "k8s", utrace.Service.Source)
	require.Equal(t, "synced", utrace.Service.SyncStatus)
	require.Equal(t, "k8s业务", utrace.Service.ServiceType)
	require.NotEmpty(t, utrace.TargetID)

	targets, err := fixture.targets.ListByService(ctx, utrace.Service.ID)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, "cloud_native_workload", targets[0].TargetType)
	require.Equal(t, "test03", targets[0].IdentityAttributes["k8s.cluster.id"])
	require.Equal(t, "logplatform", targets[0].IdentityAttributes["k8s.namespace.name"])
	require.Equal(t, "Deployment", targets[0].IdentityAttributes["k8s.workload.kind"])
	require.Equal(t, "utrace-api", targets[0].IdentityAttributes["k8s.workload.name"])
}

func TestPreviewRouteUsesClusterBoundEndpointWhenEndpointIDIsEmpty(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))
	service := fixture.createService(t, "utrace-api")
	group := fixture.createGroup(t)
	endpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-test03",
		WriteURL:  "http://vl.test03:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.test03:9428/select/logsql/query",
		VMUIURL:   "http://vl.test03:9428/select/vmui",
		ClusterID: "test03",
	})
	require.NoError(t, err)

	preview, err := fixture.service.PreviewRoute(ctx, UpsertRouteRequest{
		ServiceID:    service.ID,
		SourceType:   SourceTypeK8sStdout,
		AgentGroupID: group.ID,
		K8s: K8sSourceInput{
			ClusterID:    "test03",
			Namespace:    "logplatform",
			WorkloadKind: "Deployment",
			WorkloadName: "utrace-api",
		},
	})

	require.NoError(t, err)
	require.Equal(t, endpoint.ID, preview.Endpoint.ID)
	require.Contains(t, preview.AgentYAML, "logs_endpoint: \"http://vl.test03:9428/insert/opentelemetry/v1/logs\"")
}

func TestCreateK8sRouteAutoCreatesClusterAgentGroup(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))
	service := fixture.createService(t, "utrace-api")
	endpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-test03",
		WriteURL:  "http://vl.test03:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.test03:9428/select/logsql/query",
		VMUIURL:   "http://vl.test03:9428/select/vmui",
		ClusterID: "test03",
	})
	require.NoError(t, err)

	route, err := fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  service.ID,
		SourceType: SourceTypeK8sStdout,
		EndpointID: endpoint.ID,
		K8s: K8sSourceInput{
			ClusterID:    "test03",
			Namespace:    "logplatform",
			WorkloadKind: "Deployment",
			WorkloadName: "utrace-api",
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, route.Route.AgentGroupID)
	group, err := collectormanagement.NewService(fixture.store.CollectorGroups(), fixture.store.CollectorInstances()).GetGroup(ctx, route.Route.AgentGroupID)
	require.NoError(t, err)
	require.Equal(t, "logs-k8s-test03-novaobs-system", group.Name)
	require.Equal(t, "dedicated_collector", group.Mode)
	require.Equal(t, "test03", group.Cluster)
	require.Equal(t, "novaobs-system", group.Namespace)

	secondService := fixture.createService(t, "payment-api")
	secondRoute, err := fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  secondService.ID,
		SourceType: SourceTypeK8sStdout,
		EndpointID: endpoint.ID,
		K8s: K8sSourceInput{
			ClusterID:      "test03",
			Namespace:      "logplatform",
			AgentNamespace: "novaobs-system",
			WorkloadKind:   "Deployment",
			WorkloadName:   "payment-api",
		},
	})
	require.NoError(t, err)
	require.Equal(t, route.Route.AgentGroupID, secondRoute.Route.AgentGroupID)
}

func TestCreateVMRouteAutoCreatesHostAgentGroup(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))
	service := fixture.createService(t, "billing-api")
	endpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-vm",
		WriteURL:  "http://vl.vm:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.vm:9428/select/logsql/query",
		VMUIURL:   "http://vl.vm:9428/select/vmui",
		ScopeType: EndpointScopeVM,
	})
	require.NoError(t, err)

	route, err := fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  service.ID,
		SourceType: SourceTypeVMFile,
		EndpointID: endpoint.ID,
		VM: VMSourceInput{
			HostGroup:   "billing-vms",
			PathPattern: "/data/logs/*.log",
		},
	})

	require.NoError(t, err)
	group, err := collectormanagement.NewService(fixture.store.CollectorGroups(), fixture.store.CollectorInstances()).GetGroup(ctx, route.Route.AgentGroupID)
	require.NoError(t, err)
	require.Equal(t, "logs-vm-prod-billing-vms", group.Name)
	require.Equal(t, "shared_gateway", group.Mode)
	require.Empty(t, group.Cluster)
	require.Empty(t, group.Namespace)
}

func TestCreateRouteRejectsUnsafeCollectorYAML(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))
	service := fixture.createService(t, "billing-api")
	endpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-vm",
		WriteURL:  "http://vl.vm:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.vm:9428/select/logsql/query",
		VMUIURL:   "http://vl.vm:9428/select/vmui",
		ScopeType: EndpointScopeVM,
	})
	require.NoError(t, err)

	_, err = fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  service.ID,
		SourceType: SourceTypeVMFile,
		EndpointID: endpoint.ID,
		VM: VMSourceInput{
			HostGroup:     "billing-vms",
			PathPattern:   "/data/logs/*.log",
			CollectorYAML: "receivers:\n  filelog/custom:\n",
		},
	})
	require.ErrorContains(t, err, "collector_yaml 必须包含 service.pipelines.logs")

	_, err = fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  service.ID,
		SourceType: SourceTypeVMFile,
		EndpointID: endpoint.ID,
		VM: VMSourceInput{
			HostGroup:   "billing-vms",
			PathPattern: "/data/logs/*.log",
			CollectorYAML: validCollectorYAML(
				"http://other-vl:9428/insert/opentelemetry/v1/logs",
				"",
			),
		},
	})
	require.ErrorContains(t, err, "collector_yaml exporter 写入地址必须与当前 VictoriaLogs 端点一致")

	_, err = fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  service.ID,
		SourceType: SourceTypeVMFile,
		EndpointID: endpoint.ID,
		VM: VMSourceInput{
			HostGroup:     "billing-vms",
			PathPattern:   "/data/logs/*.log",
			CollectorYAML: validCollectorYAML(endpoint.WriteURL, "    headers:\n      authorization: Bearer plain-token\n"),
		},
	})
	require.ErrorContains(t, err, "collector_yaml 不能直接包含")
}

func TestK8sRouteRejectsCustomCollectorYAMLWhenBundleHasMultipleRoutes(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))
	endpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-test03",
		WriteURL:  "http://vl.test03:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.test03:9428/select/logsql/query",
		VMUIURL:   "http://vl.test03:9428/select/vmui",
		ClusterID: "test03",
	})
	require.NoError(t, err)
	firstService := fixture.createService(t, "utrace-api")
	secondService := fixture.createService(t, "payment-api")

	_, err = fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  firstService.ID,
		SourceType: SourceTypeK8sStdout,
		EndpointID: endpoint.ID,
		K8s: K8sSourceInput{
			ClusterID:     "test03",
			Namespace:     "logplatform",
			WorkloadKind:  "Deployment",
			WorkloadName:  "utrace-api",
			CollectorYAML: validCollectorYAML(endpoint.WriteURL, ""),
		},
	})
	require.NoError(t, err)

	_, err = fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  secondService.ID,
		SourceType: SourceTypeK8sStdout,
		EndpointID: endpoint.ID,
		K8s: K8sSourceInput{
			ClusterID:    "test03",
			Namespace:    "logplatform",
			WorkloadKind: "Deployment",
			WorkloadName: "payment-api",
		},
	})
	require.ErrorContains(t, err, "同一 K8s 采集域包含多条日志路由")
}

func TestK8sRouteRejectsAmbiguousOrVMEndpoint(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))
	service := fixture.createService(t, "utrace-api")
	group := fixture.createGroup(t)
	globalEndpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-global",
		WriteURL:  "http://vl.global:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.global:9428/select/logsql/query",
		VMUIURL:   "http://vl.global:9428/select/vmui",
		ScopeType: EndpointScopeGlobal,
	})
	require.NoError(t, err)
	vmEndpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-vm",
		WriteURL:  "http://vl.vm:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.vm:9428/select/logsql/query",
		VMUIURL:   "http://vl.vm:9428/select/vmui",
		ScopeType: EndpointScopeVM,
	})
	require.NoError(t, err)
	_, err = fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-test03",
		WriteURL:  "http://vl.test03:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.test03:9428/select/logsql/query",
		VMUIURL:   "http://vl.test03:9428/select/vmui",
		ClusterID: "test03",
	})
	require.NoError(t, err)

	req := UpsertRouteRequest{
		ServiceID:    service.ID,
		SourceType:   SourceTypeK8sStdout,
		AgentGroupID: group.ID,
		K8s: K8sSourceInput{
			ClusterID:    "test03",
			Namespace:    "logplatform",
			WorkloadKind: "Deployment",
			WorkloadName: "utrace-api",
		},
	}
	req.EndpointID = globalEndpoint.ID
	_, err = fixture.service.PreviewRoute(ctx, req)
	require.ErrorContains(t, err, "当前集群已有绑定的 VictoriaLogs 端点")

	req.EndpointID = vmEndpoint.ID
	_, err = fixture.service.PreviewRoute(ctx, req)
	require.ErrorContains(t, err, "K8s 日志路由不能选择 VM 专用 VictoriaLogs 端点")
}

func TestPublishK8sRouteCombinesRoutesAndParseRulesIntoOneDaemonSet(t *testing.T) {
	ctx := context.Background()
	deploy := &fakeDeploymentService{}
	fixture := newLogsFixtureWithDeploy(t, fakeK8sRuntimeGroups("test03", "logplatform"), deploy)
	group := fixture.createGroup(t)
	endpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-test03",
		WriteURL:  "http://vl.test03:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.test03:9428/select/logsql/query",
		VMUIURL:   "http://vl.test03:9428/select/vmui",
		ClusterID: "test03",
	})
	require.NoError(t, err)
	firstService := fixture.createService(t, "utrace-api")
	secondService := fixture.createService(t, "payment-api")

	_, err = fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:    firstService.ID,
		SourceType:   SourceTypeK8sStdout,
		AgentGroupID: group.ID,
		EndpointID:   endpoint.ID,
		K8s: K8sSourceInput{
			ClusterID:    "test03",
			Namespace:    "logplatform",
			WorkloadKind: "Deployment",
			WorkloadName: "utrace-api",
		},
	})
	require.NoError(t, err)
	secondRoute, err := fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:    secondService.ID,
		SourceType:   SourceTypeK8sStdout,
		AgentGroupID: group.ID,
		EndpointID:   endpoint.ID,
		K8s: K8sSourceInput{
			ClusterID:    "test03",
			Namespace:    "logplatform",
			WorkloadKind: "Deployment",
			WorkloadName: "payment-api",
			ParseRules: []LogParseRule{{
				Name:     "payment-text",
				RuleType: ParseRuleRegex,
				Pattern:  `^(?P<level>[A-Z]+)\s+(?P<message>.*)$`,
				Enabled:  true,
			}},
		},
	})
	require.NoError(t, err)

	result, err := fixture.service.PublishRoute(ctx, platformrbac.DevAdminSubject(), secondRoute.Route.ID, PublishRouteRequest{})

	require.NoError(t, err)
	require.True(t, result.RequiresConfirmation)
	require.Contains(t, deploy.lastPreviewYAML, "kind: DaemonSet")
	require.Contains(t, deploy.lastPreviewYAML, "name: novaobs-logs-agent")
	require.Equal(t, 1, strings.Count(deploy.lastPreviewYAML, "kind: DaemonSet"))
	require.Contains(t, deploy.lastPreviewYAML, "utrace-api")
	require.Contains(t, deploy.lastPreviewYAML, "payment-api")
	require.Contains(t, deploy.lastPreviewYAML, "ExtractPatterns(body,")
	require.Contains(t, deploy.lastPreviewYAML, "payment-text")
}

func TestPreviewParseRulesReturnsFieldsAndErrors(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))

	jsonPreview, err := fixture.service.PreviewParseRules(ctx, ParsePreviewRequest{
		Sample: `{"level":"INFO","message":"checkout ok","cost":42}`,
		ParseRules: []LogParseRule{{
			Name:     "json",
			RuleType: ParseRuleJSON,
			Enabled:  true,
		}},
	})
	require.NoError(t, err)
	require.Equal(t, "ok", jsonPreview.Status)
	require.Equal(t, "INFO", jsonPreview.Fields["level"])
	require.Equal(t, "checkout ok", jsonPreview.Fields["message"])
	require.Equal(t, float64(42), jsonPreview.Fields["cost"])
	require.Empty(t, jsonPreview.Errors)

	regexPreview, err := fixture.service.PreviewParseRules(ctx, ParsePreviewRequest{
		Sample: "WARN payment timeout",
		ParseRules: []LogParseRule{{
			Name:     "text",
			RuleType: ParseRuleRegex,
			Pattern:  `^(?P<level>[A-Z]+)\s+(?P<message>.*)$`,
			Enabled:  true,
		}},
	})
	require.NoError(t, err)
	require.Equal(t, "ok", regexPreview.Status)
	require.Equal(t, "WARN", regexPreview.Fields["level"])
	require.Equal(t, "payment timeout", regexPreview.Fields["message"])

	invalidPreview, err := fixture.service.PreviewParseRules(ctx, ParsePreviewRequest{
		Sample: "WARN payment timeout",
		ParseRules: []LogParseRule{{
			Name:     "broken",
			RuleType: ParseRuleRegex,
			Pattern:  `^([A-Z]+)\s+(.*)$`,
			Enabled:  true,
		}},
	})
	require.NoError(t, err)
	require.Equal(t, "error", invalidPreview.Status)
	require.NotEmpty(t, invalidPreview.Errors)

	disabledPreview, err := fixture.service.PreviewParseRules(ctx, ParsePreviewRequest{
		Sample: "WARN payment timeout",
		ParseRules: []LogParseRule{{
			Name:     "disabled",
			RuleType: ParseRuleRegex,
			Pattern:  `^(?P<level>[A-Z]+)\s+(?P<message>.*)$`,
			Enabled:  false,
		}},
	})
	require.NoError(t, err)
	require.Equal(t, "ok", disabledPreview.Status)
	require.Equal(t, "WARN payment timeout", disabledPreview.Fields["body"])
	require.NotContains(t, disabledPreview.Fields, "level")
}

func TestK8sDaemonSetUsesClusterStdoutIncludeAndRolloutHash(t *testing.T) {
	input := renderInput{
		ServiceName: "utrace-api",
		Environment: "prod",
		Source: LogSource{
			SourceType:   SourceTypeK8sStdout,
			ClusterID:    "test03",
			Namespace:    "logplatform",
			WorkloadKind: "Deployment",
			WorkloadName: "utrace-api",
		},
		Endpoint: LogEndpoint{WriteURL: "http://vl.test03:9428/insert/opentelemetry/v1/logs"},
		Route:    LogRoute{ID: "route-001"},
	}

	yaml, hash := renderK8sDaemonSetBundle([]renderInput{input})

	require.NotEmpty(t, hash)
	require.Contains(t, yaml, `- "/var/log/pods/*_*_*/*/*.log"`)
	require.NotContains(t, yaml, `/var/log/pods/logplatform_*_*/*/*.log`)
	require.Contains(t, yaml, `novaobs.io/config-hash: "`)
	require.Contains(t, yaml, hash)
}

func TestK8sDaemonSetEmbedsCustomCollectorYAML(t *testing.T) {
	input := renderInput{
		ServiceName: "utrace-api",
		Environment: "prod",
		Source: LogSource{
			SourceType:    SourceTypeK8sStdout,
			ClusterID:     "test03",
			Namespace:     "logplatform",
			WorkloadKind:  "Deployment",
			WorkloadName:  "utrace-api",
			CollectorYAML: validCollectorYAML("http://vl.test03:9428/insert/opentelemetry/v1/logs", ""),
		},
		Endpoint: LogEndpoint{WriteURL: "http://vl.test03:9428/insert/opentelemetry/v1/logs"},
		Route:    LogRoute{ID: "route-001"},
	}

	yaml, hash := renderK8sDaemonSetBundle([]renderInput{input})

	require.NotEmpty(t, hash)
	require.Contains(t, yaml, "collector.yaml: |")
	require.Contains(t, yaml, "    receivers:\n      filelog/custom:")
	require.Contains(t, yaml, `logs_endpoint: "http://vl.test03:9428/insert/opentelemetry/v1/logs"`)
	require.NotContains(t, yaml, "filelog/k8s")
}

func TestVMFileConfigUsesCustomCollectorYAML(t *testing.T) {
	input := renderInput{
		ServiceName: "legacy-api",
		Environment: "prod",
		Source: LogSource{
			SourceType:    SourceTypeVMFile,
			HostGroup:     "vm-prod",
			PathPattern:   "/data/logs/*.log",
			CollectorYAML: validCollectorYAML("http://vl.vm:9428/insert/opentelemetry/v1/logs", ""),
		},
		Endpoint: LogEndpoint{WriteURL: "http://vl.vm:9428/insert/opentelemetry/v1/logs"},
		Route:    LogRoute{ID: "route-vm"},
	}

	yaml, hash := renderVMFileConfig(input)

	require.NotEmpty(t, hash)
	require.Contains(t, yaml, "receivers:\n  filelog/custom:")
	require.Contains(t, yaml, `logs_endpoint: "http://vl.vm:9428/insert/opentelemetry/v1/logs"`)
	require.NotContains(t, yaml, "service.name")
}

type logsFixture struct {
	store   *memstore.Store
	repo    servicecatalog.Repository
	targets servicecatalog.TargetRepository
	service Service
}

func newLogsFixture(t *testing.T, resources K8sResourceService) logsFixture {
	t.Helper()
	return newLogsFixtureWithDeploy(t, resources, &fakeDeploymentService{})
}

func newLogsFixtureWithDeploy(t *testing.T, resources K8sResourceService, deploy K8sDeploymentService) logsFixture {
	t.Helper()
	store := memstore.NewStore()
	repo := servicecatalog.NewRepository(store.Services())
	targets := servicecatalog.NewTargetRepository(store.ServiceTargets())
	collectorSvc := collectormanagement.NewService(store.CollectorGroups(), store.CollectorInstances())
	service := NewService(
		store.LogEndpoints(),
		store.LogSources(),
		store.LogRoutes(),
		store.LogAgentPlans(),
		repo,
		targets,
		collectorSvc,
		fakeClusterService{},
		resources,
		deploy,
	)
	return logsFixture{store: store, repo: repo, targets: targets, service: service}
}

func (f logsFixture) createService(t *testing.T, name string) servicecatalog.Service {
	t.Helper()
	service, err := f.repo.Create(context.Background(), servicecatalog.Service{
		Name:         name,
		DisplayName:  name,
		Environment:  "prod",
		Cluster:      "test03",
		Namespace:    "logplatform",
		IdentityType: "k8s_workload",
		Status:       "active",
		Source:       "k8s",
		SyncStatus:   "synced",
	})
	require.NoError(t, err)
	return service
}

func (f logsFixture) createGroup(t *testing.T) collectormanagement.CollectorGroup {
	t.Helper()
	group, err := collectormanagement.NewService(f.store.CollectorGroups(), f.store.CollectorInstances()).CreateGroup(context.Background(), collectormanagement.CollectorGroup{
		Name:        "test03-logs",
		Mode:        "dedicated_collector",
		Environment: "prod",
		Cluster:     "test03",
		Namespace:   "novaobs-system",
		Status:      "active",
	})
	require.NoError(t, err)
	return group
}

func validCollectorYAML(writeURL string, exporterExtras string) string {
	return `receivers:
  filelog/custom:
    include: [/data/app.log]
exporters:
  otlphttp/victorialogs:
    logs_endpoint: "` + writeURL + `"
` + exporterExtras + `service:
  pipelines:
    logs:
      receivers: [filelog/custom]
      exporters: [otlphttp/victorialogs]
`
}

type fakeClusterService struct{}

func (fakeClusterService) List(ctx context.Context, filter k8sopscluster.ListFilter) ([]k8sopscluster.Cluster, error) {
	return []k8sopscluster.Cluster{{ID: "test03", Name: "test03", Status: "active"}}, nil
}

func (fakeClusterService) Get(ctx context.Context, id string) (k8sopscluster.Cluster, error) {
	return k8sopscluster.Cluster{ID: id, Name: id, Status: "active"}, nil
}

type fakeRuntimeGroups struct {
	response k8sopsresource.RuntimeGroupsResponse
}

func fakeK8sRuntimeGroups(clusterID string, namespace string) fakeRuntimeGroups {
	return fakeRuntimeGroups{response: k8sopsresource.RuntimeGroupsResponse{
		ClusterID: clusterID,
		Namespace: namespace,
		Groups: []k8sopsresource.RuntimeGroup{{
			Key:         "deployment/utrace-api",
			DisplayName: "utrace-api",
			Workloads: []k8sopsresource.RuntimeWorkloadNode{{
				Key:            "deployment/utrace-api",
				Name:           "utrace-api",
				Kind:           "Deployment",
				Selector:       map[string]string{"app": "utrace-api"},
				TemplateLabels: map[string]string{"app": "utrace-api", "tier": "api"},
				ServiceAccounts: []string{
					"default",
				},
				PodsSummary: k8sopsresource.RuntimePodSummary{Total: 2, Running: 2},
			}, {
				Key:            "deployment/payment-api",
				Name:           "payment-api",
				Kind:           "Deployment",
				Selector:       map[string]string{"app": "payment-api"},
				TemplateLabels: map[string]string{"app": "payment-api"},
				PodsSummary:    k8sopsresource.RuntimePodSummary{Total: 1, Running: 1},
			}},
		}},
	}}
}

func (f fakeRuntimeGroups) ListRuntimeGroups(ctx context.Context, query k8sopsresource.RuntimeGroupsQuery) (k8sopsresource.RuntimeGroupsResponse, error) {
	return f.response, nil
}

type fakeDeploymentService struct {
	lastPreviewYAML string
}

func (f *fakeDeploymentService) Preview(ctx context.Context, subject platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error) {
	f.lastPreviewYAML = req.YAMLContent
	return k8sopsdeployment.OperationResult{
		Status:            "previewed",
		Message:           "previewed",
		PreviewID:         "preview-1",
		ConfirmationToken: "confirm-1",
		AuditID:           "audit-1",
	}, nil
}

func (f *fakeDeploymentService) Apply(ctx context.Context, subject platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error) {
	return k8sopsdeployment.OperationResult{Status: "applied", Message: "applied", AuditID: "audit-2"}, nil
}
