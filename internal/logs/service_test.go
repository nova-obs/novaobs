package logs

import (
	"context"
	"fmt"
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

func TestCreateVLEndpointRejectsRootWriteURL(t *testing.T) {
	fixture := newLogsFixture(t, nil)

	_, err := fixture.service.CreateEndpoint(context.Background(), LogEndpoint{
		Name:     "vl-root",
		WriteURL: "http://vl.test03:9428/",
		QueryURL: "http://vl.test03:9428/select/logsql/query",
		VMUIURL:  "http://vl.test03:9428/select/vmui",
	})

	require.ErrorContains(t, err, "/insert/opentelemetry/v1/logs")
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
	require.Equal(t, route.Route.SourceID, secondRoute.Route.SourceID)
	require.Equal(t, "logplatform", route.Source.Namespace)
	require.Equal(t, "utrace-api", route.Source.WorkloadName)
	require.Equal(t, "payment-api", secondRoute.Source.WorkloadName)

	var sources []LogSource
	require.NoError(t, fixture.store.LogSources().FindAll(ctx, &sources))
	require.Len(t, sources, 1)
	require.Equal(t, SourceTypeK8sStdout, sources[0].SourceType)
	require.Equal(t, "test03", sources[0].ClusterID)
	require.Equal(t, "novaobs-system", sources[0].AgentNamespace)
	require.Empty(t, sources[0].Namespace)
	require.Empty(t, sources[0].WorkloadName)
}

func TestCreateK8sRouteRejectsRouteLevelCollectorYAML(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))
	service := fixture.createService(t, "mtu-test")
	endpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-test03",
		WriteURL:  "http://10.86.11.30:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://10.86.11.30:9428/select/logsql/query",
		VMUIURL:   "http://10.86.11.30:9428/select/vmui",
		ClusterID: "test03",
	})
	require.NoError(t, err)

	_, err = fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  service.ID,
		SourceType: SourceTypeK8sStdout,
		EndpointID: endpoint.ID,
		K8s: K8sSourceInput{
			ClusterID:     "test03",
			Namespace:     "mtu-test",
			WorkloadKind:  "DaemonSet",
			WorkloadName:  "mtu-ds",
			CollectorYAML: validMultiServiceK8sCollectorYAML(endpoint.WriteURL),
		},
	})

	require.ErrorContains(t, err, "K8s 日志接入不再接受 route 级 collector_yaml")
}

func TestK8sHostPathSourceTypeIsRetired(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))
	service := fixture.createService(t, "utrace-api")

	_, err := fixture.service.PreviewRoute(ctx, UpsertRouteRequest{
		ServiceID:  service.ID,
		SourceType: "k8s_hostpath",
		K8s: K8sSourceInput{
			ClusterID:    "test03",
			Namespace:    "logplatform",
			WorkloadKind: "Deployment",
			WorkloadName: "utrace-api",
			PathPattern:  "/data/logs/*.log",
		},
	})

	require.ErrorContains(t, err, "日志来源类型只支持 k8s_stdout、vm_file")
}

func TestPreviewAndUpdateK8sRouteRejectsRouteLevelCollectorYAML(t *testing.T) {
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
			CollectorYAML:  validK8sCollectorYAML(endpoint.WriteURL, "logplatform", "utrace-api"),
		},
	}
	preview, err := fixture.service.PreviewRoute(ctx, updateReq)
	require.ErrorContains(t, err, "K8s 日志接入不再接受 route 级 collector_yaml")
	require.Empty(t, preview.AgentYAML)

	updated, err := fixture.service.UpdateRoute(ctx, route.Route.ID, updateReq)
	require.ErrorContains(t, err, "K8s 日志接入不再接受 route 级 collector_yaml")
	require.Empty(t, updated.Route.ID)

	routes, err := fixture.service.ListRoutes(ctx)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Equal(t, route.Route.ID, routes[0].Route.ID)
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

func TestUpdateEndpointKeepsIDAndValidatesUniqueness(t *testing.T) {
	ctx := context.Background()
	fixture := newLogsFixture(t, fakeK8sRuntimeGroups("test03", "logplatform"))
	endpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-test03",
		SinkType:  EndpointSinkVL,
		WriteURL:  "http://vl.test03:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.test03:9428/select/logsql/query",
		VMUIURL:   "http://vl.test03:9428/select/vmui",
		ScopeType: EndpointScopeK8sCluster,
		ClusterID: "test03",
	})
	require.NoError(t, err)
	_, err = fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-prod",
		SinkType:  EndpointSinkVL,
		WriteURL:  "http://vl.prod:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.prod:9428/select/logsql/query",
		VMUIURL:   "http://vl.prod:9428/select/vmui",
		ScopeType: EndpointScopeGlobal,
	})
	require.NoError(t, err)

	updated, err := fixture.service.UpdateEndpoint(ctx, endpoint.ID, LogEndpoint{
		Name:        "vl-test03-fixed",
		Description: "fixed write path",
		SinkType:    EndpointSinkVL,
		WriteURL:    "http://vl-fixed.test03:9428/insert/opentelemetry/v1/logs",
		QueryURL:    "http://vl-fixed.test03:9428/select/logsql/query",
		VMUIURL:     "http://vl-fixed.test03:9428/select/vmui",
		ScopeType:   EndpointScopeK8sCluster,
		ClusterID:   "test03",
	})
	require.NoError(t, err)
	require.Equal(t, endpoint.ID, updated.ID)
	require.Equal(t, endpoint.CreatedAt, updated.CreatedAt)
	require.Equal(t, "vl-test03-fixed", updated.Name)
	require.Equal(t, "http://vl-fixed.test03:9428/insert/opentelemetry/v1/logs", updated.WriteURL)

	_, err = fixture.service.UpdateEndpoint(ctx, endpoint.ID, LogEndpoint{
		Name:      "vl-prod",
		SinkType:  EndpointSinkVL,
		WriteURL:  "http://vl-fixed.test03:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl-fixed.test03:9428/select/logsql/query",
		VMUIURL:   "http://vl-fixed.test03:9428/select/vmui",
		ScopeType: EndpointScopeK8sCluster,
		ClusterID: "test03",
	})
	require.ErrorContains(t, err, "日志下游端点名称已存在")

	_, err = fixture.service.UpdateEndpoint(ctx, endpoint.ID, LogEndpoint{
		Name:      "vl-root",
		SinkType:  EndpointSinkVL,
		WriteURL:  "http://vl-fixed.test03:9428/",
		QueryURL:  "http://vl-fixed.test03:9428/select/logsql/query",
		VMUIURL:   "http://vl-fixed.test03:9428/select/vmui",
		ScopeType: EndpointScopeK8sCluster,
		ClusterID: "test03",
	})
	require.ErrorContains(t, err, "/insert/opentelemetry/v1/logs")
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
	require.ErrorContains(t, err, "collector_yaml 必须包含 logs pipeline")

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

func TestValidateCollectorYAMLSupportsNamedLogsPipelines(t *testing.T) {
	endpoint := LogEndpoint{WriteURL: "http://vl.test03:9428/insert/opentelemetry/v1/logs"}
	raw := strings.Replace(validK8sCollectorYAML(endpoint.WriteURL, "logplatform", "utrace-api"), "    logs:\n", "    logs/logplatform-utrace-api:\n", 1)
	require.NoError(t, validateCollectorYAML(raw, endpoint))
}

func TestValidateCollectorYAMLSupportsMultipleNamedLogsPipelines(t *testing.T) {
	endpoint := LogEndpoint{WriteURL: "http://10.86.11.30:9428/insert/opentelemetry/v1/logs"}

	require.NoError(t, validateCollectorYAML(validMultiServiceK8sCollectorYAML(endpoint.WriteURL), endpoint))
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
	require.ErrorContains(t, err, "K8s 日志接入不再接受 route 级 collector_yaml")

	err = validateK8sCollectorYAMLRuntimeScope(globalIncludeYAML)
	require.ErrorContains(t, err, "不能使用全局 Pod 日志路径")
}

func TestK8sCollectorYAMLRejectsUnsafeNodeLogIncludes(t *testing.T) {
	for _, include := range []string{
		"/var/log",
		"/var/log/*.log",
		"/var/log/containers/*.log",
		"/var/log/pods/*/*/*.log",
		"/var/log/pods/*_*_*/*/*.log",
		"/var/lib/kubelet/pods/*/volumes/*",
		"/var/lib/docker/containers/*/*.log",
	} {
		err := validateK8sCollectorYAMLRuntimeScope(validCollectorYAMLWithInclude("http://vl.test03:9428/insert/opentelemetry/v1/logs", include, ""))
		require.Error(t, err, include)
	}

	err := validateK8sCollectorYAMLRuntimeScope(validK8sCollectorYAML("http://vl.test03:9428/insert/opentelemetry/v1/logs", "logplatform", "utrace-api"))
	require.NoError(t, err)
}

func TestK8sCollectorYAMLRequiresFilelogSafetyDefaults(t *testing.T) {
	raw := `receivers:
  file_log/novaobs:
    include:
      - /var/log/pods/logplatform_*_*/*/*.log
    start_at: end
    include_file_path: true
    include_file_name: false
processors:
  resource/novaobs:
    attributes:
      - key: service.name
        value: logplatform
        action: upsert
  batch:
exporters:
  otlp_http/logs_downstream:
    logs_endpoint: http://vl.test03:9428/insert/opentelemetry/v1/logs
service:
  pipelines:
    logs:
      receivers: [file_log/novaobs]
      processors: [resource/novaobs, batch]
      exporters: [otlp_http/logs_downstream]
`
	err := validateK8sCollectorYAMLRuntimeScope(raw)
	require.ErrorContains(t, err, "poll_interval")

	aliasReceiver := strings.Replace(raw, "file_log/novaobs", "filelog/novaobs", 1)
	err = validateK8sCollectorYAMLRuntimeScope(aliasReceiver)
	require.ErrorContains(t, err, "不支持 filelog alias")
}

func TestK8sRouteUsesCollectorDomainBundleWhenMultipleRoutes(t *testing.T) {
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

	firstRoute, err := fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  firstService.ID,
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

	secondRoute, err := fixture.service.CreateRoute(ctx, UpsertRouteRequest{
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
	require.NoError(t, err)
	require.Equal(t, firstRoute.Route.SourceID, secondRoute.Route.SourceID)
	routes, err := fixture.service.ListRoutes(ctx)
	require.NoError(t, err)
	require.Len(t, routes, 2)
	hashes := map[string]string{}
	for _, view := range routes {
		hashes[view.Route.ID] = view.Route.CollectorConfigHash
	}
	require.Equal(t, hashes[firstRoute.Route.ID], hashes[secondRoute.Route.ID])

	preview, err := fixture.service.PreviewRoute(ctx, UpsertRouteRequest{
		RouteID:    secondRoute.Route.ID,
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
	require.NoError(t, err)
	require.Contains(t, preview.AgentYAML, "file_log/logplatform-utrace-api")
	require.Contains(t, preview.AgentYAML, "file_log/logplatform-payment-api")
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
	require.NoError(t, err)

	req.EndpointID = vmEndpoint.ID
	_, err = fixture.service.PreviewRoute(ctx, req)
	require.ErrorContains(t, err, "K8s 日志路由不能选择 VM 专用日志下游端点")

	_, err = fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-test03-secondary",
		WriteURL:  "http://vl-secondary.test03:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl-secondary.test03:9428/select/logsql/query",
		VMUIURL:   "http://vl-secondary.test03:9428/select/vmui",
		ClusterID: "test03",
	})
	require.NoError(t, err)
	req.EndpointID = ""
	_, err = fixture.service.PreviewRoute(ctx, req)
	require.ErrorContains(t, err, "请显式选择端点")
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

func TestPublishK8sRouteCombinesSameClusterRoutesWithDifferentEndpoints(t *testing.T) {
	ctx := context.Background()
	deploy := &fakeDeploymentService{}
	fixture := newLogsFixtureWithDeploy(t, fakeK8sRuntimeGroups("test03", "logplatform"), deploy)
	firstEndpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-logplatform",
		WriteURL:  "http://vl-logplatform:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl-logplatform:9428/select/logsql/query",
		VMUIURL:   "http://vl-logplatform:9428/select/vmui",
		ClusterID: "test03",
	})
	require.NoError(t, err)
	secondEndpoint, err := fixture.service.CreateEndpoint(ctx, LogEndpoint{
		Name:      "vl-mtu",
		WriteURL:  "http://vl-mtu:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl-mtu:9428/select/logsql/query",
		VMUIURL:   "http://vl-mtu:9428/select/vmui",
		ClusterID: "test03",
	})
	require.NoError(t, err)
	firstService := fixture.createService(t, "prometheus")
	secondService := fixture.createService(t, "mtu-api")

	_, err = fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  firstService.ID,
		SourceType: SourceTypeK8sStdout,
		EndpointID: firstEndpoint.ID,
		K8s: K8sSourceInput{
			ClusterID:    "test03",
			Namespace:    "logplatform",
			WorkloadKind: "Deployment",
			WorkloadName: "prometheus",
		},
	})
	require.NoError(t, err)
	secondRoute, err := fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  secondService.ID,
		SourceType: SourceTypeK8sStdout,
		EndpointID: secondEndpoint.ID,
		K8s: K8sSourceInput{
			ClusterID:    "test03",
			Namespace:    "mtu-test",
			WorkloadKind: "Deployment",
			WorkloadName: "mtu-api",
			ParseRules: []LogParseRule{{
				Name:     "mtu-json",
				RuleType: ParseRuleJSON,
				Enabled:  true,
			}},
		},
	})
	require.NoError(t, err)

	result, err := fixture.service.PublishRoute(ctx, platformrbac.DevAdminSubject(), secondRoute.Route.ID, PublishRouteRequest{})

	require.NoError(t, err)
	require.True(t, result.RequiresConfirmation)
	require.Equal(t, 1, strings.Count(deploy.lastPreviewYAML, "kind: DaemonSet"))
	require.Contains(t, deploy.lastPreviewYAML, "/var/log/pods/logplatform_prometheus*_*/*/*.log")
	require.Contains(t, deploy.lastPreviewYAML, "/var/log/pods/mtu-test_mtu-api*_*/*/*.log")
	require.Contains(t, deploy.lastPreviewYAML, "logs_endpoint: \"http://vl-logplatform:9428/insert/opentelemetry/v1/logs\"")
	require.Contains(t, deploy.lastPreviewYAML, "logs_endpoint: \"http://vl-mtu:9428/insert/opentelemetry/v1/logs\"")
	require.Contains(t, deploy.lastPreviewYAML, "file_log/logplatform-prometheus")
	require.Contains(t, deploy.lastPreviewYAML, "file_log/mtu-test-mtu-api")
	require.Contains(t, deploy.lastPreviewYAML, "logs/logplatform-prometheus")
	require.Contains(t, deploy.lastPreviewYAML, "logs/mtu-test-mtu-api")
	require.Contains(t, deploy.lastPreviewYAML, "otlp_http/endpoint_vl-logplatform")
	require.Contains(t, deploy.lastPreviewYAML, "otlp_http/endpoint_vl-mtu")
	require.NotContains(t, deploy.lastPreviewYAML, "logs/route_")
	require.Contains(t, deploy.lastPreviewYAML, "ParseJSON(body)")
}

func TestPublishK8sRouteUpdatesAllRoutesInSameCollectorDomain(t *testing.T) {
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
	firstService := fixture.createService(t, "prometheus")
	secondService := fixture.createService(t, "mtu-api")

	firstRoute, err := fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  firstService.ID,
		SourceType: SourceTypeK8sStdout,
		EndpointID: endpoint.ID,
		K8s: K8sSourceInput{
			ClusterID:    "test03",
			Namespace:    "logplatform",
			WorkloadKind: "Deployment",
			WorkloadName: "prometheus",
		},
	})
	require.NoError(t, err)
	preview, err := fixture.service.PublishRoute(ctx, platformrbac.DevAdminSubject(), firstRoute.Route.ID, PublishRouteRequest{})
	require.NoError(t, err)
	_, err = fixture.service.PublishRoute(ctx, platformrbac.DevAdminSubject(), firstRoute.Route.ID, PublishRouteRequest{
		PreviewID:         preview.PreviewID,
		ConfirmationToken: preview.ConfirmationToken,
	})
	require.NoError(t, err)

	secondRoute, err := fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  secondService.ID,
		SourceType: SourceTypeK8sStdout,
		EndpointID: endpoint.ID,
		K8s: K8sSourceInput{
			ClusterID:    "test03",
			Namespace:    "logplatform",
			WorkloadKind: "Deployment",
			WorkloadName: "mtu-api",
		},
	})
	require.NoError(t, err)
	preview, err = fixture.service.PublishRoute(ctx, platformrbac.DevAdminSubject(), secondRoute.Route.ID, PublishRouteRequest{})
	require.NoError(t, err)
	applied, err := fixture.service.PublishRoute(ctx, platformrbac.DevAdminSubject(), secondRoute.Route.ID, PublishRouteRequest{
		PreviewID:         preview.PreviewID,
		ConfirmationToken: preview.ConfirmationToken,
	})
	require.NoError(t, err)

	firstAfter, err := fixture.service.ServiceRouteSummary(ctx, firstService.ID)
	require.NoError(t, err)
	require.Len(t, firstAfter, 1)
	require.Equal(t, applied.Route.CollectorConfigHash, firstAfter[0].Route.CollectorConfigHash)
	require.Equal(t, "applied", firstAfter[0].Route.LastPublishStatus)
	require.Contains(t, deploy.lastApplyReq.YAMLContent, "file_log/logplatform-prometheus")
	require.Contains(t, deploy.lastApplyReq.YAMLContent, "file_log/logplatform-mtu-api")
}

func TestK8sDeploymentManifestHashDoesNotChangeWhenCollectorConfigChanges(t *testing.T) {
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
	firstRoute, err := fixture.service.CreateRoute(ctx, UpsertRouteRequest{
		ServiceID:  firstService.ID,
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
	firstDeploymentHash := firstRoute.Source.DeploymentManifestHash

	secondService := fixture.createService(t, "payment-api")
	secondRoute, err := fixture.service.CreateRoute(ctx, UpsertRouteRequest{
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

	require.NoError(t, err)
	require.NotEqual(t, firstRoute.Route.CollectorConfigHash, secondRoute.Route.CollectorConfigHash)
	require.Equal(t, firstDeploymentHash, secondRoute.Source.DeploymentManifestHash)
}

func TestApplyK8sPublishUsesPersistedPreviewBundle(t *testing.T) {
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
	preview, err := fixture.service.PublishRoute(ctx, platformrbac.DevAdminSubject(), route.Route.ID, PublishRouteRequest{})
	require.NoError(t, err)
	previewYAML := deploy.lastPreviewReq.YAMLContent

	mutated := route.Route
	mutated.K8s.WorkloadName = "payment-api"
	require.NoError(t, fixture.store.LogRoutes().Update(ctx, mutated.ID, mutated))
	applied, err := fixture.service.PublishRoute(ctx, platformrbac.DevAdminSubject(), route.Route.ID, PublishRouteRequest{
		PreviewID:         preview.PreviewID,
		ConfirmationToken: preview.ConfirmationToken,
	})

	require.NoError(t, err)
	require.Equal(t, preview.Plan.CollectorConfigHash, applied.Plan.CollectorConfigHash)
	require.Equal(t, previewYAML, deploy.lastApplyReq.YAMLContent)
	require.Contains(t, deploy.lastApplyReq.YAMLContent, "file_log/logplatform-utrace-api")
	require.NotContains(t, deploy.lastApplyReq.YAMLContent, "file_log/logplatform-payment-api")
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

	preview, err := fixture.service.PublishRoute(ctx, platformrbac.DevAdminSubject(), route.Route.ID, PublishRouteRequest{})
	require.NoError(t, err)
	result, err := fixture.service.PublishRoute(ctx, platformrbac.DevAdminSubject(), route.Route.ID, PublishRouteRequest{
		PreviewID:         preview.PreviewID,
		ConfirmationToken: preview.ConfirmationToken,
	})

	require.NoError(t, err)
	require.Equal(t, "applied", result.Status)
	require.True(t, deploy.lastApplyReq.ForceConflicts)
	require.Equal(t, preview.PreviewID, deploy.lastApplyReq.PreviewID)
	require.Equal(t, preview.ConfirmationToken, deploy.lastApplyReq.ConfirmationToken)
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

	rendered := renderK8sDaemonSetBundleWithHashes([]renderInput{input})
	yaml := rendered.ManifestYAML
	collectorYAML, collectorHash := renderK8sCollectorYAML([]renderInput{input})

	require.NotEmpty(t, rendered.DeploymentManifestHash)
	require.NotEmpty(t, rendered.CollectorConfigHash)
	require.Equal(t, collectorHash, rendered.CollectorConfigHash)
	require.Equal(t, hashYAML(collectorYAML), rendered.CollectorConfigHash)
	require.Equal(t, hashYAML(rendered.DeploymentManifestYAML), rendered.DeploymentManifestHash)
	require.NotEqual(t, rendered.CollectorConfigHash, rendered.DeploymentManifestHash)
	require.Contains(t, yaml, `- "/var/log/pods/logplatform_utrace-api*_*/*/*.log"`)
	require.NotContains(t, yaml, `- "/var/log/pods/*_*_*/*/*.log"`)
	require.Contains(t, yaml, "file_log/logplatform-utrace-api")
	require.Contains(t, yaml, "mountPath: /data/docker/containers")
	require.Contains(t, yaml, "path: /data/docker/containers")
	require.Contains(t, yaml, "name: docker-containers")
	require.NotContains(t, yaml, `- "/data/docker/containers`)
	require.Contains(t, yaml, "otlp_http/endpoint_")
	require.Contains(t, yaml, "file_storage/filelog_offsets")
	require.NotContains(t, collectorYAML, "opamp:")
	require.NotContains(t, collectorYAML, "service:\n  extensions: [file_storage/filelog_offsets, health_check, opamp]")
	require.NotContains(t, yaml, "NOVAOBS_OPAMP_ENDPOINT")
	require.Contains(t, collectorYAML, "k8s.pod.uid: ${env:KUBE_POD_UID}")
	require.Contains(t, yaml, "health_check:")
	require.Contains(t, yaml, "endpoint: 0.0.0.0:13133")
	require.Contains(t, yaml, "service:")
	require.Contains(t, yaml, "metrics:")
	require.Contains(t, yaml, "port: 8888")
	require.Contains(t, yaml, `prometheus.io/scrape: "true"`)
	require.Contains(t, yaml, `prometheus.io/path: "/metrics"`)
	require.Contains(t, yaml, `prometheus.io/port: "8888"`)
	require.Contains(t, yaml, "poll_interval: 10s")
	require.Contains(t, yaml, "max_concurrent_files: 64")
	require.Contains(t, yaml, "max_batches: 2")
	require.Contains(t, yaml, "file_cache_advise: true")
	require.Contains(t, yaml, `- "/var/log/pods/*_novaobs-logs-agent-*_*/*/*.log"`)
	require.Contains(t, yaml, `- "/var/log/pods/*/*/*.gz"`)
	require.Contains(t, yaml, "memory_limiter")
	require.Contains(t, yaml, "storage: file_storage/filelog_offsets")
	require.Contains(t, yaml, "processors: [memory_limiter, k8s_attributes, resource/logplatform-utrace-api, batch]")
	require.Contains(t, yaml, "service:")
	require.Contains(t, yaml, "name: metrics")
	require.Contains(t, yaml, "containerPort: 8888")
	require.Contains(t, yaml, "name: health")
	require.Contains(t, yaml, "containerPort: 13133")
	require.Contains(t, yaml, "readinessProbe:")
	require.Contains(t, yaml, "livenessProbe:")
	require.Contains(t, yaml, "NOVAOBS_CLUSTER_ID")
	require.Contains(t, yaml, "NOVAOBS_COLLECTOR_GROUP_ID")
	require.Contains(t, yaml, `novaobs.io/config-hash: "`)
	require.Contains(t, yaml, rendered.CollectorConfigHash)
}

func TestK8sDaemonSetRendersOpAMPOnlyWhenEndpointConfigured(t *testing.T) {
	input := renderInput{
		ServiceName: "utrace-api",
		Environment: "prod",
		Source: LogSource{
			SourceType:     SourceTypeK8sStdout,
			ClusterID:      "test03",
			Namespace:      "logplatform",
			WorkloadKind:   "Deployment",
			WorkloadName:   "utrace-api",
			AgentNamespace: "novaobs-system",
		},
		Endpoint: LogEndpoint{WriteURL: "http://vl.test03:9428/insert/opentelemetry/v1/logs"},
		Route:    LogRoute{ID: "route-001"},
	}

	defaultYAML, _ := renderK8sDaemonSetBundle([]renderInput{input})
	require.NotContains(t, defaultYAML, "opamp:")
	require.NotContains(t, defaultYAML, "NOVAOBS_OPAMP_ENDPOINT")

	input.Deployment.OpAMPEndpoint = "ws://novaobs.example.com/v1/opamp"
	enabledYAML, _ := renderK8sDaemonSetBundle([]renderInput{input})
	collectorYAML, _ := renderK8sCollectorYAML([]renderInput{input})

	require.Contains(t, collectorYAML, "opamp:")
	require.Contains(t, collectorYAML, "endpoint: ${env:NOVAOBS_OPAMP_ENDPOINT}")
	require.Contains(t, collectorYAML, "service:\n  extensions: [file_storage/filelog_offsets, health_check, opamp]")
	require.Contains(t, enabledYAML, "NOVAOBS_OPAMP_ENDPOINT")
	require.Contains(t, enabledYAML, `value: "ws://novaobs.example.com/v1/opamp"`)
}

func TestRouteCollectorConfigReturnsCollectorYAMLForK8sHash(t *testing.T) {
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
	require.NotEmpty(t, route.Route.CollectorConfigHash)
	require.NotNil(t, route.Source)
	require.NotEmpty(t, route.Source.DeploymentManifestHash)
	require.NotEmpty(t, route.Source.CollectorConfigHash)
	require.Equal(t, route.Route.CollectorConfigHash, route.Source.CollectorConfigHash)
	require.NotEqual(t, route.Source.CollectorConfigHash, route.Source.DeploymentManifestHash)

	config, err := fixture.service.RouteCollectorConfig(ctx, route.Route.ID)

	require.NoError(t, err)
	require.Equal(t, route.Route.ID, config.RouteID)
	require.Equal(t, route.Route.CollectorConfigHash, config.CollectorConfigHash)
	require.Equal(t, route.Source.DeploymentManifestHash, config.DeploymentManifestHash)
	require.Equal(t, SourceTypeK8sStdout, config.SourceType)
	require.Contains(t, config.CollectorYAML, "receivers:")
	require.Contains(t, config.CollectorYAML, "file_log/logplatform-utrace-api")
	require.NotContains(t, config.CollectorYAML, "kind: DaemonSet")
	require.NotContains(t, config.CollectorYAML, "collector.yaml: |")
}

func TestRouteCollectorConfigReadsPersistedCollectorYAMLByHash(t *testing.T) {
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
	originalHash := route.Route.CollectorConfigHash

	mutated := route.Route
	mutated.K8s.WorkloadName = "payment-api"
	require.NoError(t, fixture.store.LogRoutes().Update(ctx, mutated.ID, mutated))

	config, err := fixture.service.RouteCollectorConfig(ctx, route.Route.ID)

	require.NoError(t, err)
	require.Equal(t, originalHash, config.CollectorConfigHash)
	require.Contains(t, config.CollectorYAML, "file_log/logplatform-utrace-api")
	require.NotContains(t, config.CollectorYAML, "file_log/logplatform-payment-api")
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
	require.Contains(t, rendered, "mountPath: /data/docker/containers")
	require.NotContains(t, rendered, "mountPath: /var/log/containers")
	require.NotContains(t, rendered, "mountPath: /var/lib/docker/containers")
	require.NotContains(t, rendered, "DirectoryOrCreate")
	require.Contains(t, rendered, "type: Directory")
	require.Contains(t, rendered, "requests:")
	require.Contains(t, rendered, "limits:")
	require.Contains(t, rendered, "runAsUser: 0")
	require.Contains(t, rendered, "runAsGroup: 0")
	require.Contains(t, rendered, `drop: ["ALL"]`)
	require.Contains(t, rendered, "readOnlyRootFilesystem: true")
	require.Contains(t, rendered, "mountPath: /var/lib/otelcol")
	require.Contains(t, rendered, "name: offset-storage")
	require.Equal(t, []string{
		"Namespace",
		"ServiceAccount",
		"ClusterRole",
		"ClusterRoleBinding",
		"ConfigMap",
		"Service",
		"DaemonSet",
	}, renderedKinds(t, rendered))
}

func TestK8sDaemonSetUsesManagedCollectorYAMLForK8sSources(t *testing.T) {
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
	require.Contains(t, yaml, "collector.yaml: |")
	require.Contains(t, yaml, "    receivers:\n      file_log/logplatform-utrace-api:")
	require.Contains(t, yaml, `logs_endpoint: "http://vl.test03:9428/insert/opentelemetry/v1/logs"`)
	require.NotContains(t, yaml, "file_log/custom")
}

func TestVMFileConfigUsesCustomCollectorYAML(t *testing.T) {
	input := renderInput{
		ServiceName: "legacy-api",
		Environment: "prod",
		Source: LogSource{
			SourceType:          SourceTypeVMFile,
			HostGroup:           "vm-prod",
			PathPattern:         "/data/logs/*.log",
			CustomCollectorYAML: validCollectorYAML("http://vl.vm:9428/insert/opentelemetry/v1/logs", ""),
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
		store.LogCollectorConfigVersions(),
		store.LogDeploymentManifestVersions(),
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
	return validCollectorYAMLWithInclude(writeURL, "/data/app.log", exporterExtras)
}

func validK8sCollectorYAML(writeURL string, namespace string, workload string) string {
	include := fmt.Sprintf("/var/log/pods/%s_%s*_*/*/*.log", namespace, workload)
	return `extensions:
  file_storage/filelog_offsets:
    directory: /var/lib/otelcol/filelog_offsets
    create_directory: true
receivers:
  file_log/custom:
    include: [` + include + `]
    exclude:
      - /var/log/pods/*_novaobs-logs-agent-*_*/*/*.log
      - /var/log/pods/*/*/*.gz
      - /var/log/pods/*/*/*.tmp
      - /var/log/pods/*/*/*.log.*
    poll_interval: 10s
    max_concurrent_files: 128
    max_batches: 2
    max_log_size: 1MiB
    file_cache_advise: true
    include_file_path: true
    include_file_name: false
    start_at: end
    storage: file_storage/filelog_offsets
processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: 512
    spike_limit_mib: 128
  k8s_attributes:
    auth_type: serviceAccount
    passthrough: false
    filter:
      node_from_env_var: KUBE_NODE_NAME
  batch:
exporters:
  otlp_http/victorialogs:
    logs_endpoint: "` + writeURL + `"
service:
  extensions: [file_storage/filelog_offsets]
  pipelines:
    logs:
      receivers: [file_log/custom]
      processors: [memory_limiter, k8s_attributes, batch]
      exporters: [otlp_http/victorialogs]
`
}

func validMultiServiceK8sCollectorYAML(writeURL string) string {
	return `extensions:
  file_storage/filelog_offsets:
    directory: /var/lib/otelcol/filelog_offsets
    create_directory: true
receivers:
  file_log/logplatform-prometheus:
    include:
      - /var/log/pods/logplatform_prometheus*_*/*/*.log
    exclude:
      - /var/log/pods/*_novaobs-logs-agent-*_*/*/*.log
      - /var/log/pods/*/*/*.gz
      - /var/log/pods/*/*/*.tmp
      - /var/log/pods/*/*/*.log.*
    poll_interval: 10s
    max_concurrent_files: 128
    max_batches: 2
    max_log_size: 1MiB
    file_cache_advise: true
    include_file_path: true
    include_file_name: false
    start_at: end
    storage: file_storage/filelog_offsets
  file_log/mtu-test-mtu-ds:
    include:
      - /var/log/pods/mtu-test_mtu-ds*_*/*/*.log
    exclude:
      - /var/log/pods/*_novaobs-logs-agent-*_*/*/*.log
      - /var/log/pods/*/*/*.gz
      - /var/log/pods/*/*/*.tmp
      - /var/log/pods/*/*/*.log.*
    poll_interval: 10s
    max_concurrent_files: 128
    max_batches: 2
    max_log_size: 1MiB
    file_cache_advise: true
    include_file_path: true
    include_file_name: false
    start_at: end
    storage: file_storage/filelog_offsets
processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: 512
    spike_limit_mib: 128
  k8s_attributes:
    auth_type: serviceAccount
    passthrough: false
    filter:
      node_from_env_var: KUBE_NODE_NAME
  resource/logplatform-prometheus:
    attributes:
      - key: service.name
        value: logplatform
        action: upsert
  resource/mtu-test-mtu-ds:
    attributes:
      - key: service.name
        value: mtu-test
        action: upsert
  batch:
    timeout: 1s
exporters:
  otlp_http/logs_downstream:
    logs_endpoint: ` + writeURL + `
service:
  extensions: [file_storage/filelog_offsets]
  pipelines:
    logs/logplatform-prometheus:
      receivers: [file_log/logplatform-prometheus]
      processors: [memory_limiter, k8s_attributes, resource/logplatform-prometheus, batch]
      exporters: [otlp_http/logs_downstream]
    logs/mtu-test-mtu-ds:
      receivers: [file_log/mtu-test-mtu-ds]
      processors: [memory_limiter, k8s_attributes, resource/mtu-test-mtu-ds, batch]
      exporters: [otlp_http/logs_downstream]
`
}

func validCollectorYAMLWithInclude(writeURL string, include string, exporterExtras string) string {
	return `receivers:
  file_log/custom:
    include: [` + include + `]
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
