package alerting

import (
	"context"
	"strings"
	"testing"
	"time"

	"novaobs/internal/database/memstore"
	"novaobs/internal/logs"
	k8sopsdeployment "novaobs/internal/modules/k8sops/deployment"
	"novaobs/internal/platform/audit"
	platformrbac "novaobs/internal/platform/rbac"

	"github.com/stretchr/testify/require"
)

func TestLogRuntimePublishesVmalertManifestForEndpointAndMarksRuntimeRulesApplied(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	endpoint := logs.LogEndpoint{
		ID:        "vl-prod",
		Name:      "prod-logs",
		SinkType:  logs.EndpointSinkVL,
		QueryURL:  "http://vmauth.internal/customer-a/logs/select/logsql/query",
		ScopeType: logs.EndpointScopeK8sCluster,
		ClusterID: "prod-1",
	}
	require.NoError(t, store.LogEndpoints().Insert(ctx, endpoint))
	repository := NewStoreRepository(store.Alerting())
	now := time.Date(2026, 6, 26, 9, 0, 0, 0, time.UTC)
	require.NoError(t, repository.SavePolicy(ctx, time.Time{}, NotificationPolicy{
		ID: "pay-team-oncall", Name: "pay", Receiver: "pay-oncall", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}, audit.Event{ID: "audit-policy", CreatedAt: now}))
	spec := validRuleSpec()
	spec.Scope.EndpointID = endpoint.ID
	spec.Scope.AccountID = "1001"
	spec.Scope.ProjectID = "2001"
	require.NoError(t, repository.SaveChange(ctx, ChangeSet{
		Rule:   Rule{ID: "rule-a", Spec: spec, State: RuleStateEnabled, ApplyStatus: ApplyStatusPending, CurrentUpdateID: "update-a", CreatedAt: now, UpdatedAt: now},
		Update: UpdateRecord{ID: "update-a", RuleID: "rule-a", Action: UpdateActionCreate, ResultingState: RuleStateEnabled, Spec: spec, CreatedAt: now},
		Audit:  audit.Event{ID: "audit-a", CreatedAt: now},
	}))
	deployments := &recordingRuntimeDeploymentService{}
	service := NewLogRuntimeService(LogRuntimeDependencies{
		Endpoints: store.LogEndpoints(), Repository: repository, K8sDeployments: deployments,
		DefaultAlertIngestURL: "http://novaobs-api.novaobs-system.svc.cluster.local:8080",
		Clock:                 func() time.Time { return now },
	})

	preview, err := service.Publish(ctx, platformrbac.DevAdminSubject(), endpoint.ID, LogRuntimePublishRequest{})
	require.NoError(t, err)
	require.True(t, preview.RequiresConfirmation)
	require.Equal(t, "preview-1", preview.PreviewID)
	require.Equal(t, "http://vmauth.internal/customer-a/logs", preview.DatasourceURL)
	require.Contains(t, deployments.lastPreview.YAMLContent, "image: hub-test.service.ucloud.cn/logsplatfrom/vmalert:v1.145.0")
	require.Contains(t, deployments.lastPreview.YAMLContent, "-datasource.url=http://vmauth.internal/customer-a/logs")
	require.Contains(t, deployments.lastPreview.YAMLContent, "-notifier.url=http://novaobs-api.novaobs-system.svc.cluster.local:8080")
	require.Contains(t, deployments.lastPreview.YAMLContent, "runtime.yaml: |")
	require.Contains(t, deployments.lastPreview.YAMLContent, "novaobs_runtime_id: vmalert-logs:vl-prod")
	require.Contains(t, deployments.lastPreview.YAMLContent, "AccountID: 1001")
	require.Contains(t, deployments.lastPreview.YAMLContent, "ProjectID: 2001")

	applied, err := service.Publish(ctx, platformrbac.DevAdminSubject(), endpoint.ID, LogRuntimePublishRequest{
		ClusterID: endpoint.ClusterID, Namespace: "novaobs-system", PreviewID: preview.PreviewID, ConfirmationToken: preview.ConfirmationToken,
	})
	require.NoError(t, err)
	require.False(t, applied.RequiresConfirmation)
	require.Equal(t, 1, applied.AppliedRules)
	require.Equal(t, "preview-1", deployments.lastApply.PreviewID)

	var rule Rule
	require.NoError(t, store.Alerting().FindRuleByID(ctx, "rule-a", &rule))
	require.Equal(t, ApplyStatusApplied, rule.ApplyStatus)
	require.Equal(t, "update-a", rule.AppliedUpdateID)
}

func TestLogRuntimeRejectsClusterOverrideOutsideEndpointBinding(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	require.NoError(t, store.LogEndpoints().Insert(ctx, logs.LogEndpoint{
		ID: "vl-prod", Name: "prod", SinkType: logs.EndpointSinkVL, QueryURL: "http://vl/select/logsql/query", ScopeType: logs.EndpointScopeK8sCluster, ClusterID: "prod-1",
	}))
	service := NewLogRuntimeService(LogRuntimeDependencies{
		Endpoints: store.LogEndpoints(), Repository: NewStoreRepository(store.Alerting()), K8sDeployments: &recordingRuntimeDeploymentService{},
		DefaultAlertIngestURL: "http://novaobs-api.novaobs-system.svc.cluster.local:8080",
	})

	_, err := service.Publish(ctx, platformrbac.DevAdminSubject(), "vl-prod", LogRuntimePublishRequest{ClusterID: "prod-2"})
	require.ErrorContains(t, err, "日志端点绑定的 K8s 集群")
}

func TestLogRuntimeRejectsNamespaceOutsideFixedPlatformNamespace(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	require.NoError(t, store.LogEndpoints().Insert(ctx, logs.LogEndpoint{
		ID: "vl-prod", Name: "prod", SinkType: logs.EndpointSinkVL, QueryURL: "http://vl/select/logsql/query", ScopeType: logs.EndpointScopeK8sCluster, ClusterID: "prod-1",
	}))
	service := NewLogRuntimeService(LogRuntimeDependencies{
		Endpoints: store.LogEndpoints(), Repository: NewStoreRepository(store.Alerting()), K8sDeployments: &recordingRuntimeDeploymentService{},
		DefaultAlertIngestURL: "http://novaobs-api.novaobs-system.svc.cluster.local:8080",
	})

	_, err := service.Publish(ctx, platformrbac.DevAdminSubject(), "vl-prod", LogRuntimePublishRequest{Namespace: "business-ns"})
	require.ErrorContains(t, err, "固定 namespace novaobs-system")
}

func TestLogRuntimeRejectsNonClusterVictoriaLogsEndpoint(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	require.NoError(t, store.LogEndpoints().Insert(ctx, logs.LogEndpoint{
		ID: "vl-global", Name: "global", SinkType: logs.EndpointSinkVL, QueryURL: "http://vl/select/logsql/query", ScopeType: logs.EndpointScopeGlobal,
	}))
	service := NewLogRuntimeService(LogRuntimeDependencies{
		Endpoints: store.LogEndpoints(), Repository: NewStoreRepository(store.Alerting()), K8sDeployments: &recordingRuntimeDeploymentService{},
	})

	_, err := service.Publish(ctx, platformrbac.DevAdminSubject(), "vl-global", LogRuntimePublishRequest{AlertIngestURL: "http://novaobs-api:8080"})
	require.ErrorContains(t, err, "K8s 集群级 VictoriaLogs 端点")
}

type recordingRuntimeDeploymentService struct {
	lastPreview k8sopsdeployment.OperationRequest
	lastApply   k8sopsdeployment.OperationRequest
}

func (s *recordingRuntimeDeploymentService) Preview(_ context.Context, _ platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error) {
	s.lastPreview = req
	return runtimeOperationResult("previewed", "preview-1", "confirm-1"), nil
}

func (s *recordingRuntimeDeploymentService) Apply(_ context.Context, _ platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error) {
	s.lastApply = req
	return runtimeOperationResult("applied", req.PreviewID, ""), nil
}

func runtimeOperationResult(status string, previewID string, token string) k8sopsdeployment.OperationResult {
	name := "novaobs-vmalert-prod-logs"
	return k8sopsdeployment.OperationResult{
		Status: status, Message: status, PreviewID: previewID, ConfirmationToken: token, AuditID: "audit-runtime",
		Resources: []k8sopsdeployment.ResourceIdentity{{
			ClusterID: "prod-1", APIVersion: "v1", Kind: "Namespace", Name: "novaobs-system",
		}, {
			ClusterID: "prod-1", Namespace: "novaobs-system", APIVersion: "apps/v1", Kind: "Deployment", Name: name,
		}},
		Warnings: []string{},
	}
}

func TestVictoriaLogsDatasourceURLKeepsVmauthPrefix(t *testing.T) {
	got, err := victoriaLogsDatasourceURL("http://vmauth.example.com/customer-a/logs/select/logsql/query?x=1")
	require.NoError(t, err)
	require.Equal(t, "http://vmauth.example.com/customer-a/logs", got)
	got, err = victoriaLogsDatasourceURL("http://victorialogs:9428/select/logsql/query")
	require.NoError(t, err)
	require.Equal(t, "http://victorialogs:9428", got)
	require.False(t, strings.Contains(got, "logsql"))
}
