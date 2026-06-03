package logs

import (
	"context"
	"io"
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
	"gopkg.in/yaml.v3"
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

func TestPreviewAndUpdateK8sRouteWithCollectorYAMLExcludeCurrentRoute(t *testing.T) {
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

	updateReq := UpsertRouteRequest{
		RouteID:    route.Route.ID,
		ServiceID:  service.ID,
		SourceType: SourceTypeK8sStdout,
		EndpointID: endpoint.ID,
		K8s: K8sSourceInput{
			ClusterID:      "test03",
			Namespace:      "logplatform",
			AgentNamespace: "novaobs-system",
			WorkloadKind:   "Deployment",
			WorkloadName:   "utrace-api",
			CollectorYAML:  validCollectorYAML(endpoint.WriteURL, ""),
		},
	}
	preview, err := fixture.service.PreviewRoute(ctx, updateReq)
	require.NoError(t, err)
	require.Contains(t, preview.AgentYAML, "file_log/custom")

	updated, err := fixture.service.UpdateRoute(ctx, route.Route.ID, updateReq)
	require.NoError(t, err)
	require.Equal(t, route.Route.ID, updated.Route.ID)
	require.Equal(t, route.Route.SourceID, updated.Route.SourceID)
	require.Equal(t, "pending_publish", updated.Route.LastPublishStatus)
	require.Contains(t, updated.Source.CollectorYAML, "file_log/custom")

	routes, err := fixture.service.ListRoutes(ctx)
	require.NoError(t, err)
	require.Len(t, routes, 1)
}

func TestCreateVMRouteAutoCreatesHostAgentGroup(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))
	service := fixture.createVMService(t, "billing-api")
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

func TestRouteRejectsServiceIdentityMismatchedWithSource(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))
	k8sService := fixture.createService(t, "utrace-api")
	vmService := fixture.createVMService(t, "billing-api")
	endpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "logs-downstream",
		WriteURL:  "http://vl.test03:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.test03:9428/select/logsql/query",
		VMUIURL:   "http://vl.test03:9428/select/vmui",
		ScopeType: EndpointScopeGlobal,
	})
	require.NoError(t, err)

	_, err = fixture.service.PreviewRoute(ctx, UpsertRouteRequest{
		ServiceID:  k8sService.ID,
		SourceType: SourceTypeVMFile,
		EndpointID: endpoint.ID,
		VM: VMSourceInput{
			HostGroup:   "billing-vms",
			PathPattern: "/data/logs/*.log",
		},
	})
	require.ErrorContains(t, err, "VM 日志接入只能选择 VM/物理机服务")

	_, err = fixture.service.PreviewRoute(ctx, UpsertRouteRequest{
		ServiceID:  vmService.ID,
		SourceType: SourceTypeK8sStdout,
		EndpointID: endpoint.ID,
		K8s: K8sSourceInput{
			ClusterID:    "test03",
			Namespace:    "logplatform",
			WorkloadKind: "Deployment",
			WorkloadName: "billing-api",
		},
	})
	require.ErrorContains(t, err, "K8s 日志接入只能选择 K8s 服务")
}

func TestCreateEndpointSupportsFlexibleDownstreamTypes(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))

	vlEndpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:     "vl-prod",
		SinkType: EndpointSinkVL,
		WriteURL: "http://vl.prod:9428/insert/opentelemetry/v1/logs",
		QueryURL: "http://vl.prod:9428/select/logsql/query",
		VMUIURL:  "http://vl.prod:9428/select/vmui",
	})
	require.NoError(t, err)
	require.Equal(t, EndpointSinkVL, vlEndpoint.SinkType)

	esEndpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:       "es-prod",
		SinkType:   EndpointSinkES,
		StreamName: "novaobs-logs",
		WriteURL:   "http://elasticsearch.prod:9200",
		ScopeType:  EndpointScopeVM,
	})
	require.NoError(t, err)
	require.Equal(t, EndpointSinkES, esEndpoint.SinkType)
	require.Equal(t, "novaobs-logs", esEndpoint.StreamName)

	kafkaEndpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:       "kafka-prod",
		SinkType:   EndpointSinkKafka,
		StreamName: "novaobs-logs",
		WriteURL:   "kafka-0.prod:9092,kafka-1.prod:9092",
		ScopeType:  EndpointScopeVM,
	})
	require.NoError(t, err)
	require.Equal(t, EndpointSinkKafka, kafkaEndpoint.SinkType)

	_, err = fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "kafka-missing-topic",
		SinkType:  EndpointSinkKafka,
		WriteURL:  "kafka-0.prod:9092",
		ScopeType: EndpointScopeVM,
	})
	require.ErrorContains(t, err, "Kafka 下游端点必须填写 topic")
}

func TestCreateRouteRejectsUnsafeCollectorYAML(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))
	service := fixture.createVMService(t, "billing-api")
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
			CollectorYAML: "receivers:\n  file_log/custom:\n",
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
	require.ErrorContains(t, err, "collector_yaml exporter 写入地址必须与当前日志下游端点一致")

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

func TestValidateCollectorYAMLSupportsESAndKafkaExporters(t *testing.T) {
	esEndpoint := LogEndpoint{SinkType: EndpointSinkES, WriteURL: "http://elasticsearch.prod:9200"}
	require.NoError(t, validateCollectorYAML(validESCollectorYAML(esEndpoint.WriteURL), esEndpoint))

	kafkaEndpoint := LogEndpoint{SinkType: EndpointSinkKafka, WriteURL: "kafka-0.prod:9092,kafka-1.prod:9092", StreamName: "novaobs-logs"}
	require.NoError(t, validateCollectorYAML(validKafkaCollectorYAML([]string{"kafka-0.prod:9092", "kafka-1.prod:9092"}), kafkaEndpoint))

	err := validateCollectorYAML(validKafkaCollectorYAML([]string{"kafka-other.prod:9092"}), kafkaEndpoint)
	require.ErrorContains(t, err, "collector_yaml exporter 写入地址必须与当前日志下游端点一致")
}

func TestK8sCollectorYAMLRejectsGlobalPodLogInclude(t *testing.T) {
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
	globalIncludeYAML := strings.ReplaceAll(validCollectorYAML(endpoint.WriteURL, ""), "/data/app.log", "/var/log/pods/*_*_*/*/*.log")

	_, err = fixture.service.PreviewRoute(ctx, UpsertRouteRequest{
		ServiceID:  service.ID,
		SourceType: SourceTypeK8sStdout,
		EndpointID: endpoint.ID,
		K8s: K8sSourceInput{
			ClusterID:      "test03",
			Namespace:      "logplatform",
			AgentNamespace: "novaobs-system",
			WorkloadKind:   "Deployment",
			WorkloadName:   "utrace-api",
			CollectorYAML:  globalIncludeYAML,
		},
	})
	require.ErrorContains(t, err, "不能使用全局 Pod 日志路径")

	err = validateK8sBundleCollectorYAML([]renderInput{{
		ServiceName: "utrace-api",
		Environment: "prod",
		Source: LogSource{
			SourceType:    SourceTypeK8sStdout,
			ClusterID:     "test03",
			Namespace:     "logplatform",
			WorkloadKind:  "Deployment",
			WorkloadName:  "utrace-api",
			CollectorYAML: globalIncludeYAML,
		},
		Endpoint: endpoint,
		Route:    LogRoute{ID: "route-001"},
	}})
	require.ErrorContains(t, err, "不能使用全局 Pod 日志路径")
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
	require.ErrorContains(t, err, "当前集群已有绑定的日志下游端点")

	req.EndpointID = vmEndpoint.ID
	_, err = fixture.service.PreviewRoute(ctx, req)
	require.ErrorContains(t, err, "K8s 日志路由不能选择 VM 专用日志下游端点")
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
	require.Len(t, result.Resources, 2)
	require.Len(t, result.Diffs, 2)
	require.True(t, deploy.lastPreviewReq.ForceConflicts)
	require.Contains(t, deploy.lastPreviewYAML, "kind: DaemonSet")
	require.Contains(t, deploy.lastPreviewYAML, "name: novaobs-logs-agent")
	require.Equal(t, 1, strings.Count(deploy.lastPreviewYAML, "kind: DaemonSet"))
	require.Contains(t, deploy.lastPreviewYAML, "utrace-api")
	require.Contains(t, deploy.lastPreviewYAML, "payment-api")
	require.Contains(t, deploy.lastPreviewYAML, "ExtractPatterns(body,")
	require.Contains(t, deploy.lastPreviewYAML, "payment-text")
}

func TestPublishK8sRouteApplyForcesManagedFieldConflicts(t *testing.T) {
	ctx := context.Background()
	deploy := &fakeDeploymentService{}
	fixture := newLogsFixtureWithDeploy(t, fakeK8sRuntimeGroups("test03", "logplatform"), deploy)
	endpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-test03",
		WriteURL:  "http://vl.test03:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.test03:9428/select/logsql/query",
		VMUIURL:   "http://vl.test03:9428/select/vmui",
		ClusterID: "test03",
	})
	require.NoError(t, err)
	service := fixture.createService(t, "utrace-api")
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

	result, err := fixture.service.PublishRoute(ctx, platformrbac.DevAdminSubject(), route.Route.ID, PublishRouteRequest{
		PreviewID:         "preview-1",
		ConfirmationToken: "confirm-1",
	})

	require.NoError(t, err)
	require.Equal(t, "applied", result.Status)
	require.True(t, deploy.lastApplyReq.ForceConflicts)
	require.Equal(t, "preview-1", deploy.lastApplyReq.PreviewID)
	require.Equal(t, "confirm-1", deploy.lastApplyReq.ConfirmationToken)
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
	require.Contains(t, yaml, `- "/var/log/pods/logplatform_utrace-api*_*/*/*.log"`)
	require.NotContains(t, yaml, `- "/var/log/pods/*_*_*/*/*.log"`)
	require.Contains(t, yaml, "file_log/k8s")
	require.Contains(t, yaml, "otlp_http/logs_downstream")
	require.Contains(t, yaml, "file_storage/filelog_offsets")
	require.Contains(t, yaml, "poll_interval: 5s")
	require.Contains(t, yaml, "storage: file_storage/filelog_offsets")
	require.Contains(t, yaml, `novaobs.io/config-hash: "`)
	require.Contains(t, yaml, hash)
}

func TestK8sDaemonSetTemplateRendersValidResourceBundle(t *testing.T) {
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

	rendered, _ := renderK8sDaemonSetBundle([]renderInput{input})

	require.Contains(t, k8sDaemonSetBundleTemplateSource, "kind: DaemonSet")
	require.Contains(t, rendered, "mountPath: /var/log/pods")
	require.Contains(t, rendered, "mountPath: /var/log/containers")
	require.Contains(t, rendered, "mountPath: /var/lib/docker/containers")
	require.Contains(t, rendered, "mountPath: /var/lib/otelcol")
	require.Contains(t, rendered, "name: offset-storage")
	require.Equal(t, []string{
		"Namespace",
		"ServiceAccount",
		"ClusterRole",
		"ClusterRoleBinding",
		"ConfigMap",
		"DaemonSet",
	}, renderedKinds(t, rendered))
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
	require.Contains(t, yaml, "    receivers:\n      file_log/custom:")
	require.Contains(t, yaml, `logs_endpoint: "http://vl.test03:9428/insert/opentelemetry/v1/logs"`)
	require.NotContains(t, yaml, "file_log/k8s")
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
	require.Contains(t, yaml, "receivers:\n  file_log/custom:")
	require.Contains(t, yaml, `logs_endpoint: "http://vl.vm:9428/insert/opentelemetry/v1/logs"`)
	require.NotContains(t, yaml, "service.name")
}

func TestRenderConfigUsesDownstreamExporterByEndpointType(t *testing.T) {
	vmInput := renderInput{
		ServiceName: "billing-api",
		Environment: "prod",
		Source: LogSource{
			SourceType:  SourceTypeVMFile,
			HostGroup:   "vm-prod",
			PathPattern: "/data/logs/*.log",
		},
		Route: LogRoute{ID: "route-vm"},
	}

	esInput := vmInput
	esInput.Endpoint = LogEndpoint{SinkType: EndpointSinkES, WriteURL: "http://elasticsearch.prod:9200", StreamName: "novaobs-logs"}
	esYAML, _ := renderVMFileConfig(esInput)
	require.Contains(t, esYAML, "elasticsearch/logs_downstream:")
	require.Contains(t, esYAML, `- "http://elasticsearch.prod:9200"`)
	require.Contains(t, esYAML, `logs_index: "novaobs-logs"`)
	require.Contains(t, esYAML, "exporters: [elasticsearch/logs_downstream]")

	kafkaInput := vmInput
	kafkaInput.Endpoint = LogEndpoint{SinkType: EndpointSinkKafka, WriteURL: "kafka-0.prod:9092,kafka-1.prod:9092", StreamName: "novaobs-logs"}
	kafkaYAML, _ := renderVMFileConfig(kafkaInput)
	require.Contains(t, kafkaYAML, "kafka/logs_downstream:")
	require.Contains(t, kafkaYAML, `- "kafka-0.prod:9092"`)
	require.Contains(t, kafkaYAML, `- "kafka-1.prod:9092"`)
	require.Contains(t, kafkaYAML, `topic: "novaobs-logs"`)
	require.Contains(t, kafkaYAML, "exporters: [kafka/logs_downstream]")
}

func renderedKinds(t *testing.T, raw string) []string {
	t.Helper()
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	kinds := []string{}
	for {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if len(doc) == 0 {
			continue
		}
		kind, ok := doc["kind"].(string)
		require.True(t, ok, "rendered YAML document must include kind")
		kinds = append(kinds, kind)
	}
	return kinds
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

func (f logsFixture) createVMService(t *testing.T, name string) servicecatalog.Service {
	t.Helper()
	service, err := f.repo.Create(context.Background(), servicecatalog.Service{
		Name:         name,
		DisplayName:  name,
		Environment:  "prod",
		OwnerTeam:    "sre",
		IdentityType: "host_process",
		ServiceType:  "VM/物理机业务",
		Status:       "active",
		Source:       "manual",
		SyncStatus:   "local",
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
  file_log/custom:
    include: [/data/app.log]
exporters:
  otlp_http/victorialogs:
    logs_endpoint: "` + writeURL + `"
` + exporterExtras + `service:
  pipelines:
    logs:
      receivers: [file_log/custom]
      exporters: [otlp_http/victorialogs]
`
}

func validESCollectorYAML(writeURL string) string {
	return `receivers:
  file_log/custom:
    include: [/data/app.log]
exporters:
  elasticsearch/logs_downstream:
    endpoints:
      - "` + writeURL + `"
service:
  pipelines:
    logs:
      receivers: [file_log/custom]
      exporters: [elasticsearch/logs_downstream]
`
}

func validKafkaCollectorYAML(brokers []string) string {
	lines := []string{
		"receivers:",
		"  file_log/custom:",
		"    include: [/data/app.log]",
		"exporters:",
		"  kafka/logs_downstream:",
		"    brokers:",
	}
	for _, broker := range brokers {
		lines = append(lines, `      - "`+broker+`"`)
	}
	lines = append(lines,
		`    topic: "novaobs-logs"`,
		"service:",
		"  pipelines:",
		"    logs:",
		"      receivers: [file_log/custom]",
		"      exporters: [kafka/logs_downstream]",
		"",
	)
	return strings.Join(lines, "\n")
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
	lastPreviewReq  k8sopsdeployment.OperationRequest
	lastApplyReq    k8sopsdeployment.OperationRequest
}

func (f *fakeDeploymentService) Preview(ctx context.Context, subject platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error) {
	f.lastPreviewYAML = req.YAMLContent
	f.lastPreviewReq = req
	return k8sopsdeployment.OperationResult{
		Status:            "previewed",
		Message:           "previewed",
		PreviewID:         "preview-1",
		ConfirmationToken: "confirm-1",
		AuditID:           "audit-1",
		Resources: []k8sopsdeployment.ResourceIdentity{{
			ClusterID:  "test03",
			APIVersion: "v1",
			Kind:       "Namespace",
			Name:       "novaobs-system",
		}, {
			ClusterID:  "test03",
			Namespace:  "novaobs-system",
			APIVersion: "apps/v1",
			Kind:       "DaemonSet",
			Name:       "novaobs-logs-agent",
		}},
		Diffs: []k8sopsdeployment.ResourceDiff{{
			ClusterID:  "test03",
			APIVersion: "v1",
			Kind:       "Namespace",
			Name:       "novaobs-system",
			Operation:  "apply",
			AfterHash:  "namespace-hash",
		}, {
			ClusterID:  "test03",
			Namespace:  "novaobs-system",
			APIVersion: "apps/v1",
			Kind:       "DaemonSet",
			Name:       "novaobs-logs-agent",
			Operation:  "apply",
			AfterHash:  "daemonset-hash",
		}},
	}, nil
}

func (f *fakeDeploymentService) Apply(ctx context.Context, subject platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error) {
	f.lastApplyReq = req
	return k8sopsdeployment.OperationResult{Status: "applied", Message: "applied", AuditID: "audit-2"}, nil
}
