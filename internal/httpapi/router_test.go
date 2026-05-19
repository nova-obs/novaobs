package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"novaobs/internal/alerting"
	"novaobs/internal/collectorconfig"
	"novaobs/internal/collectormanagement"
	"novaobs/internal/database/memstore"
	"novaobs/internal/logquery"
	"novaobs/internal/modules/k8sops"
	"novaobs/internal/onboarding"
	"novaobs/internal/opamp"
	"novaobs/internal/servicecatalog"

	"github.com/gin-gonic/gin"
	"github.com/open-telemetry/opamp-go/protobufs"
	"github.com/stretchr/testify/require"
)

type testEnv struct {
	router  http.Handler
	store   *memstore.Store
	service servicecatalog.Service
	group   collectormanagement.CollectorGroup
	manager *opamp.Manager
}

func newTestRouter(t *testing.T) testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := memstore.NewStore()
	ctx := context.Background()
	svcRepo := servicecatalog.NewRepository(store.Services())
	svc, err := svcRepo.Create(ctx, servicecatalog.Service{
		Name:        "orders-api",
		DisplayName: "订单服务",
		Environment: "production",
		Cluster:     "prod-1",
		Namespace:   "orders",
		OwnerTeam:   "orders-team",
		AlertRoute:  "orders-alerts",
		Status:      "active",
	})
	require.NoError(t, err)
	collectorSvc := collectormanagement.NewService(store.CollectorGroups(), store.CollectorInstances(), collectormanagement.WithConfigVersionStore(store.CollectorConfigVersions()))
	group, err := collectorSvc.CreateGroup(ctx, collectormanagement.CollectorGroup{
		Name:        "logs-gateway",
		Mode:        "shared_gateway",
		Environment: "production",
		Cluster:     "prod-1",
		Status:      "active",
	})
	require.NoError(t, err)
	configSvc := collectorconfig.NewService(
		store.CollectorPlatformTemplates(),
		store.CollectorGroupOverrides(),
		store.ServiceEnrichmentPatches(),
		store.ServiceParserRules(),
		store.ServicePipelinePatches(),
		collectorSvc,
		svcRepo,
	)
	manager := opamp.NewManager()
	router := NewRouter(Dependencies{
		Store:                  store,
		ServiceRepo:            svcRepo,
		ServiceTargetRepo:      servicecatalog.NewTargetRepository(store.ServiceTargets()),
		CollectorConfigService: configSvc,
		CollectorService:       collectorSvc,
		OnboardingService:      onboarding.NewService(store.Onboardings(), store.IngestionIdentities(), svcRepo, collectorSvc),
		LogQueryService:        logquery.NewService(),
		AlertService:           alerting.NewService(store.AlertRules()),
		K8sOpsModule:           k8sops.NewModule(),
		OpAMPManager:           manager,
	})
	return testEnv{router: router, store: store, service: svc, group: group, manager: manager}
}

func TestRouterServesCoreAPIs(t *testing.T) {
	env := newTestRouter(t)

	for _, path := range []string{
		"/api/v1/health",
		"/api/v1/services",
		"/api/v1/services/" + env.service.ID,
		"/api/v1/services/" + env.service.ID + "/observability-graph",
		"/api/v1/services/" + env.service.ID + "/onboarding",
		"/api/v1/logs?service=orders-api&level=error",
		"/api/v1/k8sops/dashboard?cluster_id=prod",
		"/api/v1/k8s/clusters?q=prod",
		"/api/v1/k8s/namespaces?cluster_id=prod",
		"/api/v1/k8s/resources?cluster_id=prod&namespace=orders",
		"/api/v1/k8s/resources/detail?cluster_id=prod&namespace=orders&api_version=apps/v1&kind=Deployment&name=orders-api&uid=uid-orders-api",
		"/api/v1/k8s/resources/yaml?cluster_id=prod&namespace=orders&api_version=apps/v1&kind=Deployment&name=orders-api&uid=uid-orders-api",
		"/api/v1/k8s/pod-logs?cluster_id=prod&namespace=orders&pod=orders-api-6f7d&container=app",
		"/api/v1/k8s/deployment-history?cluster_id=prod",
		"/api/v1/k8s/audit-events?cluster_id=prod",
		"/api/v1/k8s/certificates?cluster_id=prod",
		"/api/v1/k8s/service-accounts?cluster_id=prod&namespace=orders",
		"/api/v1/opamp/agents",
		"/api/v1/alert-rules",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		env.router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusOK, recorder.Code, path)
		require.Contains(t, recorder.Body.String(), `"success":true`, path)
	}
}

func TestRouterReturnsServiceObservabilityGraph(t *testing.T) {
	env := newTestRouter(t)
	ctx := context.Background()
	targetRepo := servicecatalog.NewTargetRepository(env.store.ServiceTargets())
	_, err := targetRepo.Create(ctx, servicecatalog.ObservedTarget{
		ServiceID:   env.service.ID,
		TargetType:  "host_process",
		Environment: "production",
		DisplayName: "orders-api on vm-01",
		IdentityAttributes: map[string]string{
			"host.name":               "vm-01",
			"process.executable.name": "orders-api",
		},
	})
	require.NoError(t, err)

	collectorSvc := collectormanagement.NewService(env.store.CollectorGroups(), env.store.CollectorInstances())
	_, err = collectorSvc.UpsertInstance(ctx, "collector-orders", env.group.ID, collectormanagement.InstanceStatus{
		ServiceID:           env.service.ID,
		Online:              true,
		Healthy:             true,
		RemoteConfigCapable: true,
		LastSeenAt:          time.Now().UTC(),
	})
	require.NoError(t, err)

	alertSvc := alerting.NewService(env.store.AlertRules())
	_, err = alertSvc.Create(ctx, alerting.Rule{
		Name:       "orders-error-count",
		Query:      `service.name="orders-api" level=error`,
		OwnerTeam:  "orders-team",
		AlertRoute: "orders-alerts",
		Status:     "active",
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/services/"+env.service.ID+"/observability-graph", nil)
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"targets"`)
	require.Contains(t, recorder.Body.String(), `"target_type":"host_process"`)
	require.Contains(t, recorder.Body.String(), `"agents"`)
	require.Contains(t, recorder.Body.String(), `"instance_uid":"collector-orders"`)
	require.Contains(t, recorder.Body.String(), `"pipelines"`)
	require.Contains(t, recorder.Body.String(), `"alert_rules"`)
	require.Contains(t, recorder.Body.String(), `"orders-error-count"`)
	require.NotContains(t, recorder.Body.String(), `"dashboard_panels"`)
}

func TestRouterCreatesServiceTarget(t *testing.T) {
	env := newTestRouter(t)

	body := `{"target_type":"physical_or_network_device","environment":"production","display_name":"edge-fw-01","identity_attributes":{"device.name":"edge-fw-01","net.host.ip":"10.0.0.8"},"match_rules":{"syslog.hostname":"edge-fw-01"}}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/services/"+env.service.ID+"/targets", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"target_type":"physical_or_network_device"`)
	require.Contains(t, recorder.Body.String(), `"device.name":"edge-fw-01"`)

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/services/"+env.service.ID+"/targets", nil)
	env.router.ServeHTTP(listRecorder, listRequest)

	require.Equal(t, http.StatusOK, listRecorder.Code)
	require.Contains(t, listRecorder.Body.String(), `"display_name":"edge-fw-01"`)
}

func TestRouterImportsTemplateConfiguresServiceAndPublishesGroup(t *testing.T) {
	env := newTestRouter(t)

	env.manager.RegisterInstanceGroup("collector-a", env.group.ID)
	env.manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid:  []byte("collector-a"),
		Capabilities: uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig | protobufs.AgentCapabilities_AgentCapabilities_ReportsEffectiveConfig),
		Health:       &protobufs.ComponentHealth{Healthy: true, StartTimeUnixNano: uint64(time.Now().UnixNano())},
		EffectiveConfig: &protobufs.EffectiveConfig{ConfigMap: &protobufs.AgentConfigMap{ConfigMap: map[string]*protobufs.AgentConfigFile{
			"collector.yaml": {Body: []byte("receivers:\n  otlp:\nprocessors:\n  memory_limiter:\n  batch:\nservice:\n  pipelines:\n    logs:\n      receivers: [otlp]\n      processors: [memory_limiter, batch]\n      exporters: [debug]\n")},
		}}},
	})

	importBody := `{"name":"platform-prod","source_agent_uid":"collector-a","collector_group_id":"` + env.group.ID + `"}`
	importRecorder := httptest.NewRecorder()
	importRequest := httptest.NewRequest(http.MethodPost, "/api/v1/collector-platform-templates/import-from-agent", bytes.NewBufferString(importBody))
	importRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(importRecorder, importRequest)
	require.Equal(t, http.StatusCreated, importRecorder.Code)

	enrichmentBody := `{"collector_group_id":"` + env.group.ID + `"}`
	enrichmentRecorder := httptest.NewRecorder()
	enrichmentRequest := httptest.NewRequest(http.MethodPost, "/api/v1/services/"+env.service.ID+"/enrichment-patch/regenerate", bytes.NewBufferString(enrichmentBody))
	enrichmentRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(enrichmentRecorder, enrichmentRequest)
	require.Equal(t, http.StatusOK, enrichmentRecorder.Code)

	parserBody := `{"collector_group_id":"` + env.group.ID + `","parse_mode":"regex","regex_pattern":"order_id=(?P<order_id>[\\w-]+)","sample_log":"INFO order_id=o-1","enabled":true}`
	parserRecorder := httptest.NewRecorder()
	parserRequest := httptest.NewRequest(http.MethodPut, "/api/v1/services/"+env.service.ID+"/parser-rule", bytes.NewBufferString(parserBody))
	parserRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(parserRecorder, parserRequest)
	require.Equal(t, http.StatusOK, parserRecorder.Code)

	patchRecorder := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPost, "/api/v1/services/"+env.service.ID+"/parser-rule/generate-patch", nil)
	env.router.ServeHTTP(patchRecorder, patchRequest)
	require.Equal(t, http.StatusOK, patchRecorder.Code)

	validateRecorder := httptest.NewRecorder()
	validateRequest := httptest.NewRequest(http.MethodPost, "/api/v1/collector-groups/"+env.group.ID+"/config/validate", nil)
	env.router.ServeHTTP(validateRecorder, validateRequest)
	require.Equal(t, http.StatusOK, validateRecorder.Code)
	require.Contains(t, validateRecorder.Body.String(), "transform/enrich")
	require.Contains(t, validateRecorder.Body.String(), "ExtractPatterns")
	require.NotContains(t, validateRecorder.Body.String(), "filelog/")

	publishRecorder := httptest.NewRecorder()
	publishRequest := httptest.NewRequest(http.MethodPost, "/api/v1/collector-groups/"+env.group.ID+"/config/publish", nil)
	env.router.ServeHTTP(publishRecorder, publishRequest)
	require.Equal(t, http.StatusOK, publishRecorder.Code)
	require.Contains(t, publishRecorder.Body.String(), `"status":"pending"`)
}

func TestRouterActivatesCollectorGroupAfterReadinessChecks(t *testing.T) {
	env := newTestRouter(t)
	draft := "draft"
	err := env.store.CollectorGroups().Update(context.Background(), env.group.ID, collectormanagement.CollectorGroup{
		ID:          env.group.ID,
		Name:        env.group.Name,
		Mode:        env.group.Mode,
		Environment: env.group.Environment,
		Cluster:     env.group.Cluster,
		Status:      draft,
	})
	require.NoError(t, err)
	env.manager.RegisterInstanceGroup("collector-a", env.group.ID)
	env.manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid: []byte("collector-a"),
		Health:      &protobufs.ComponentHealth{Healthy: true, StartTimeUnixNano: uint64(time.Now().UnixNano())},
		EffectiveConfig: &protobufs.EffectiveConfig{ConfigMap: &protobufs.AgentConfigMap{ConfigMap: map[string]*protobufs.AgentConfigFile{
			"collector.yaml": {Body: []byte("receivers:\n  otlp:\nservice:\n  pipelines:\n    logs:\n      receivers: [otlp]\n      exporters: [debug]\n")},
		}}},
	})
	enrichmentBody := `{"collector_group_id":"` + env.group.ID + `"}`
	enrichmentRecorder := httptest.NewRecorder()
	enrichmentRequest := httptest.NewRequest(http.MethodPost, "/api/v1/services/"+env.service.ID+"/enrichment-patch/regenerate", bytes.NewBufferString(enrichmentBody))
	enrichmentRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(enrichmentRecorder, enrichmentRequest)
	require.Equal(t, http.StatusOK, enrichmentRecorder.Code)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/collector-groups/"+env.group.ID+"/activate", nil)
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"status":"active"`)
}

func TestRouterReturnsOpAMPAgentDetailWithConfigSources(t *testing.T) {
	env := newTestRouter(t)
	env.manager.RegisterInstanceGroup("collector-a", env.group.ID)
	env.manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid:  []byte("collector-a"),
		Capabilities: uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig | protobufs.AgentCapabilities_AgentCapabilities_ReportsEffectiveConfig),
		AgentDescription: &protobufs.AgentDescription{
			IdentifyingAttributes: []*protobufs.KeyValue{
				{Key: "service.name", Value: &protobufs.AnyValue{Value: &protobufs.AnyValue_StringValue{StringValue: "otelcol-contrib"}}},
			},
		},
		EffectiveConfig: &protobufs.EffectiveConfig{ConfigMap: &protobufs.AgentConfigMap{ConfigMap: map[string]*protobufs.AgentConfigFile{
			"collector.yaml": {Body: []byte("receivers:\n  otlp:\n")},
		}}},
	})

	importBody := `{"name":"platform-prod","source_agent_uid":"collector-a","collector_group_id":"` + env.group.ID + `"}`
	importRecorder := httptest.NewRecorder()
	importRequest := httptest.NewRequest(http.MethodPost, "/api/v1/collector-platform-templates/import-from-agent", bytes.NewBufferString(importBody))
	importRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(importRecorder, importRequest)
	require.Equal(t, http.StatusCreated, importRecorder.Code)

	require.NoError(t, env.store.Onboardings().Upsert(context.Background(), env.service.ID, onboarding.ServiceOnboarding{
		ServiceID:        env.service.ID,
		Mode:             "shared_gateway",
		CollectorGroupID: env.group.ID,
		Status:           "pending_verification",
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/opamp/agents/collector-a", nil)
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"instance_uid":"collector-a"`)
	require.Contains(t, recorder.Body.String(), `"config_sources"`)
	require.Contains(t, recorder.Body.String(), `"name":"orders-api"`)
}

func TestRouterRejectsInvalidParserPreview(t *testing.T) {
	env := newTestRouter(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/services/"+env.service.ID+"/parser-rule/preview", bytes.NewBufferString(`{"parse_mode":"regex","regex_pattern":"[","sample_log":"x"}`))
	request.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestRouterCreatesResources(t *testing.T) {
	env := newTestRouter(t)

	for _, item := range []struct {
		path string
		body string
	}{
		{path: "/api/v1/services", body: `{"name":"inventory-api","environment":"prod","owner_team":"inventory-team","alert_route":"inventory-prod"}`},
		{path: "/api/v1/alert-rules", body: `{"name":"inventory-error-count","rule_type":"count","source":"logs","query":"level=error"}`},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, item.path, bytes.NewBufferString(item.body))
		request.Header.Set("Content-Type", "application/json")
		env.router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusCreated, recorder.Code, item.path)
		var body map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body), item.path)
		require.Equal(t, true, body["success"], item.path)
	}
}

func TestRouterPublishesServicePipelineToBoundAgents(t *testing.T) {
	env := newTestRouter(t)
	env.manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid:  []byte("collector-a"),
		Capabilities: uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig | protobufs.AgentCapabilities_AgentCapabilities_ReportsEffectiveConfig),
		Health:       &protobufs.ComponentHealth{Healthy: true, StartTimeUnixNano: uint64(time.Now().UnixNano())},
		EffectiveConfig: &protobufs.EffectiveConfig{ConfigMap: &protobufs.AgentConfigMap{ConfigMap: map[string]*protobufs.AgentConfigFile{
			"collector.yaml": {Body: []byte("receivers:\n  otlp:\nservice:\n  pipelines:\n    logs:\n      receivers: [otlp]\n      exporters: [debug]\n")},
		}}},
	})

	assignRecorder := httptest.NewRecorder()
	assignRequest := httptest.NewRequest(http.MethodPost, "/api/v1/opamp/instances/collector-a/service", bytes.NewBufferString(`{"service_id":"`+env.service.ID+`"}`))
	assignRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(assignRecorder, assignRequest)
	require.Equal(t, http.StatusOK, assignRecorder.Code)
	require.Contains(t, assignRecorder.Body.String(), `"service_id":"`+env.service.ID+`"`)

	agentsRecorder := httptest.NewRecorder()
	agentsRequest := httptest.NewRequest(http.MethodGet, "/api/v1/services/"+env.service.ID+"/agents", nil)
	env.router.ServeHTTP(agentsRecorder, agentsRequest)
	require.Equal(t, http.StatusOK, agentsRecorder.Code)
	require.Contains(t, agentsRecorder.Body.String(), `"instance_uid":"collector-a"`)

	baseBody := `{"base_yaml":""}`
	baseRecorder := httptest.NewRecorder()
	baseRequest := httptest.NewRequest(http.MethodPut, "/api/v1/services/"+env.service.ID+"/pipeline/base", bytes.NewBufferString(baseBody))
	baseRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(baseRecorder, baseRequest)
	require.Equal(t, http.StatusOK, baseRecorder.Code)

	enrichmentRecorder := httptest.NewRecorder()
	enrichmentRequest := httptest.NewRequest(http.MethodPost, "/api/v1/services/"+env.service.ID+"/pipeline/enrichment/regenerate", nil)
	env.router.ServeHTTP(enrichmentRecorder, enrichmentRequest)
	require.Equal(t, http.StatusOK, enrichmentRecorder.Code)
	require.Contains(t, enrichmentRecorder.Body.String(), "transform/enrich")

	parserRecorder := httptest.NewRecorder()
	parserRequest := httptest.NewRequest(http.MethodPut, "/api/v1/services/"+env.service.ID+"/pipeline/parser-rule", bytes.NewBufferString(`{"parse_mode":"regex","regex_pattern":"order_id=(?P<order_id>[\\w-]+)","sample_log":"INFO order_id=o-1","enabled":true}`))
	parserRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(parserRecorder, parserRequest)
	require.Equal(t, http.StatusOK, parserRecorder.Code)

	patchRecorder := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPost, "/api/v1/services/"+env.service.ID+"/pipeline/parser-rule/generate-patch", nil)
	env.router.ServeHTTP(patchRecorder, patchRequest)
	require.Equal(t, http.StatusOK, patchRecorder.Code)

	sourcesRecorder := httptest.NewRecorder()
	sourcesRequest := httptest.NewRequest(http.MethodGet, "/api/v1/services/"+env.service.ID+"/pipeline/sources", nil)
	env.router.ServeHTTP(sourcesRecorder, sourcesRequest)
	require.Equal(t, http.StatusOK, sourcesRecorder.Code)
	require.Contains(t, sourcesRecorder.Body.String(), `"enrichment"`)
	require.Contains(t, sourcesRecorder.Body.String(), `"parser"`)
	require.Contains(t, sourcesRecorder.Body.String(), `"rendered_yaml"`)

	publishRecorder := httptest.NewRecorder()
	publishRequest := httptest.NewRequest(http.MethodPost, "/api/v1/services/"+env.service.ID+"/pipeline/publish", nil)
	env.router.ServeHTTP(publishRecorder, publishRequest)
	require.Equal(t, http.StatusOK, publishRecorder.Code)
	require.Contains(t, publishRecorder.Body.String(), `"service_id":"`+env.service.ID+`"`)
	require.Contains(t, publishRecorder.Body.String(), `"active_delivery_count":1`)
}

func TestRouterReturnsServiceBoundAgentDetail(t *testing.T) {
	env := newTestRouter(t)
	env.manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid:  []byte("collector-service-a"),
		Capabilities: uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig | protobufs.AgentCapabilities_AgentCapabilities_ReportsEffectiveConfig),
		AgentDescription: &protobufs.AgentDescription{
			IdentifyingAttributes: []*protobufs.KeyValue{
				{Key: "service.name", Value: &protobufs.AnyValue{Value: &protobufs.AnyValue_StringValue{StringValue: "otelcol-contrib"}}},
			},
		},
		EffectiveConfig: &protobufs.EffectiveConfig{ConfigMap: &protobufs.AgentConfigMap{ConfigMap: map[string]*protobufs.AgentConfigFile{
			"collector.yaml": {Body: []byte("receivers:\n  otlp:\n")},
		}}},
	})

	assignRecorder := httptest.NewRecorder()
	assignRequest := httptest.NewRequest(http.MethodPost, "/api/v1/opamp/instances/collector-service-a/service", bytes.NewBufferString(`{"service_id":"`+env.service.ID+`"}`))
	assignRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(assignRecorder, assignRequest)
	require.Equal(t, http.StatusOK, assignRecorder.Code)

	baseRecorder := httptest.NewRecorder()
	baseRequest := httptest.NewRequest(http.MethodPut, "/api/v1/services/"+env.service.ID+"/pipeline/base", bytes.NewBufferString(`{"base_yaml":""}`))
	baseRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(baseRecorder, baseRequest)
	require.Equal(t, http.StatusOK, baseRecorder.Code)

	enrichmentRecorder := httptest.NewRecorder()
	enrichmentRequest := httptest.NewRequest(http.MethodPost, "/api/v1/services/"+env.service.ID+"/pipeline/enrichment/regenerate", nil)
	env.router.ServeHTTP(enrichmentRecorder, enrichmentRequest)
	require.Equal(t, http.StatusOK, enrichmentRecorder.Code)

	detailRecorder := httptest.NewRecorder()
	detailRequest := httptest.NewRequest(http.MethodGet, "/api/v1/opamp/agents/collector-service-a", nil)
	env.router.ServeHTTP(detailRecorder, detailRequest)
	require.Equal(t, http.StatusOK, detailRecorder.Code)
	require.Contains(t, detailRecorder.Body.String(), `"service_id":"`+env.service.ID+`"`)
	require.Contains(t, detailRecorder.Body.String(), `"name":"orders-api"`)

}
