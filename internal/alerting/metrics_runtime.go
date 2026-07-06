package alerting

import (
	"context"
	"net/url"
	"strings"
	"time"

	"novaobs/internal/database"
	"novaobs/internal/logs"
	k8sopsdeployment "novaobs/internal/modules/k8sops/deployment"
	platformimages "novaobs/internal/platform/images"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/pkg/apperr"
)

type MetricsRuntimeDependencies = LogRuntimeDependencies

type MetricsRuntimeService struct {
	endpoints             database.LogEndpointStore
	repository            LogRuntimeRepository
	k8sDeployments        LogRuntimeDeploymentService
	imageTemplates        LogRuntimeImageTemplateService
	defaultAlertIngestURL string
	clock                 func() time.Time
}

func NewMetricsRuntimeService(deps MetricsRuntimeDependencies) MetricsRuntimeService {
	clock := deps.Clock
	if clock == nil {
		clock = time.Now
	}
	return MetricsRuntimeService{
		endpoints:             deps.Endpoints,
		repository:            deps.Repository,
		k8sDeployments:        deps.K8sDeployments,
		imageTemplates:        deps.ImageTemplates,
		defaultAlertIngestURL: strings.TrimSpace(deps.DefaultAlertIngestURL),
		clock:                 clock,
	}
}

func (s MetricsRuntimeService) Publish(ctx context.Context, subject platformrbac.Subject, endpointID string, req LogRuntimePublishRequest) (LogRuntimePublishResult, error) {
	if s.endpoints == nil || s.repository == nil || s.k8sDeployments == nil {
		return LogRuntimePublishResult{}, ErrUnavailable
	}
	endpointID = strings.TrimSpace(endpointID)
	if endpointID == "" {
		return LogRuntimePublishResult{}, apperr.InvalidRequest("指标端点 ID 不能为空")
	}
	var endpoint logs.LogEndpoint
	if err := s.endpoints.FindByID(ctx, endpointID, &endpoint); err != nil {
		return LogRuntimePublishResult{}, mapStoreError(err)
	}
	runtime, err := newMetricsRuntimeSpec(endpoint, req, s.defaultAlertIngestURL)
	if err != nil {
		return LogRuntimePublishResult{}, err
	}
	rules, err := s.repository.ListRuntimeRules(ctx, runtime.RuntimeID)
	if err != nil {
		return LogRuntimePublishResult{}, err
	}
	artifact, err := CompileVmalertArtifact(runtime.RuntimeID, rules, s.clock().UTC())
	if err != nil {
		return LogRuntimePublishResult{}, err
	}
	templateValues := platformimages.DefaultTemplateValues
	if s.imageTemplates != nil {
		templateValues, err = s.imageTemplates.TemplateValues(ctx)
		if err != nil {
			return LogRuntimePublishResult{}, err
		}
	}
	manifest := platformimages.ApplyTemplateValues(renderLogRuntimeManifest(runtime, artifact), templateValues)
	manifestHash := hashText(manifest)
	operation := k8sopsdeployment.OperationRequest{
		ClusterID:      runtime.ClusterID,
		YAMLContent:    manifest,
		ForceConflicts: true,
	}
	var deployed k8sopsdeployment.OperationResult
	appliedRules := 0
	requiresConfirmation := true
	if strings.TrimSpace(req.PreviewID) == "" || strings.TrimSpace(req.ConfirmationToken) == "" {
		deployed, err = s.k8sDeployments.Preview(ctx, subject, operation)
		if err != nil {
			return LogRuntimePublishResult{}, err
		}
	} else {
		operation.PreviewID = strings.TrimSpace(req.PreviewID)
		operation.ConfirmationToken = strings.TrimSpace(req.ConfirmationToken)
		deployed, err = s.k8sDeployments.Apply(ctx, subject, operation)
		if err != nil {
			return LogRuntimePublishResult{}, err
		}
		requiresConfirmation = false
		appliedRules, err = s.repository.MarkRuntimeRulesApplied(ctx, runtime.RuntimeID, s.clock().UTC())
		if err != nil {
			return LogRuntimePublishResult{}, err
		}
	}
	return LogRuntimePublishResult{
		RuntimeID:            runtime.RuntimeID,
		EndpointID:           endpoint.ID,
		ClusterID:            runtime.ClusterID,
		Namespace:            runtime.Namespace,
		DatasourceURL:        runtime.DatasourceURL,
		AlertIngestURL:       runtime.AlertIngestURL,
		ArtifactHash:         artifact.Hash,
		ManifestHash:         manifestHash,
		ManifestYAML:         manifest,
		Status:               deployed.Status,
		Message:              deployed.Message,
		RequiresConfirmation: requiresConfirmation,
		PreviewID:            deployed.PreviewID,
		ConfirmationToken:    deployed.ConfirmationToken,
		AuditID:              deployed.AuditID,
		AppliedRules:         appliedRules,
		Resources:            deployed.Resources,
		Diffs:                deployed.Diffs,
		Warnings:             deployed.Warnings,
	}, nil
}

func newMetricsRuntimeSpec(endpoint logs.LogEndpoint, req LogRuntimePublishRequest, defaultAlertIngestURL string) (logRuntimeSpec, error) {
	endpoint.ID = strings.TrimSpace(endpoint.ID)
	endpoint.Name = strings.TrimSpace(endpoint.Name)
	endpoint.Kind = strings.ToLower(strings.TrimSpace(endpoint.Kind))
	endpoint.ScopeType = strings.TrimSpace(endpoint.ScopeType)
	endpoint.ClusterID = strings.TrimSpace(endpoint.ClusterID)
	endpoint.QueryURL = strings.TrimSpace(endpoint.QueryURL)
	if endpoint.ScopeType == "" && endpoint.ClusterID != "" {
		endpoint.ScopeType = logs.EndpointScopeK8sCluster
	}
	if endpoint.Kind != "victoriametrics" || !logEndpointSignalTypesContain(endpoint.SignalTypes, logs.EndpointSignalMetrics) {
		return logRuntimeSpec{}, apperr.InvalidRequest("只有 VictoriaMetrics 指标端点可以部署 metrics vmalert Runtime")
	}
	if endpoint.ScopeType != logs.EndpointScopeK8sCluster || endpoint.ClusterID == "" {
		return logRuntimeSpec{}, apperr.InvalidRequest("metrics vmalert Runtime 必须绑定 K8s 集群级 VictoriaMetrics 端点")
	}
	clusterID := strings.TrimSpace(req.ClusterID)
	if clusterID == "" {
		clusterID = endpoint.ClusterID
	}
	if clusterID != endpoint.ClusterID {
		return logRuntimeSpec{}, apperr.InvalidRequest("metrics vmalert Runtime 必须部署到指标端点绑定的 K8s 集群")
	}
	datasourceURL, err := victoriaMetricsDatasourceURL(endpoint.QueryURL)
	if err != nil {
		return logRuntimeSpec{}, err
	}
	alertIngestURL := strings.TrimSpace(req.AlertIngestURL)
	if alertIngestURL == "" {
		alertIngestURL = strings.TrimSpace(defaultAlertIngestURL)
	}
	if alertIngestURL == "" {
		return logRuntimeSpec{}, apperr.InvalidRequest("部署 metrics vmalert Runtime 必须填写 NovaObs Alert Ingest 地址")
	}
	if err := validateHTTPURL(alertIngestURL, "NovaObs Alert Ingest 地址"); err != nil {
		return logRuntimeSpec{}, err
	}
	if namespace := strings.TrimSpace(req.Namespace); namespace != "" && namespace != defaultVmalertNamespace {
		return logRuntimeSpec{}, apperr.InvalidRequest("vmalert Runtime 只能部署到固定 namespace " + defaultVmalertNamespace)
	}
	runtimeID := "vmalert-metrics:" + endpoint.ID
	name := dnsName("novaobs-vmalert-metrics-" + firstNonEmpty(endpoint.Name, endpoint.ID))
	return logRuntimeSpec{
		RuntimeID:      runtimeID,
		Name:           name,
		EndpointID:     endpoint.ID,
		ClusterID:      clusterID,
		Namespace:      defaultVmalertNamespace,
		DatasourceURL:  datasourceURL,
		AlertIngestURL: alertIngestURL,
	}, nil
}

func victoriaMetricsDatasourceURL(queryURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(queryURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", apperr.InvalidRequest("VictoriaMetrics 查询地址必须是完整的 http/https 地址")
	}
	path := strings.TrimRight(parsed.Path, "/")
	for _, suffix := range []string{"/api/v1/query_range", "/api/v1/query", "/prometheus/api/v1/query_range", "/prometheus/api/v1/query", "/vmui"} {
		if strings.HasSuffix(path, suffix) {
			path = strings.TrimRight(strings.TrimSuffix(path, suffix), "/")
			break
		}
	}
	parsed.Path = path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func logEndpointSignalTypesContain(values []string, signalType string) bool {
	signalType = strings.ToLower(strings.TrimSpace(signalType))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == signalType {
			return true
		}
	}
	return false
}
