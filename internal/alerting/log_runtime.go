package alerting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"novaobs/internal/database"
	"novaobs/internal/logs"
	k8sopsdeployment "novaobs/internal/modules/k8sops/deployment"
	platformimages "novaobs/internal/platform/images"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/pkg/apperr"

	"gopkg.in/yaml.v3"
)

const (
	defaultVmalertNamespace = "novaobs-system"
)

type LogRuntimeRepository interface {
	ListRuntimeRules(ctx context.Context, runtimeID string) ([]Rule, error)
	MarkRuntimeRulesApplied(ctx context.Context, runtimeID string, appliedAt time.Time) (int, error)
}

type LogRuntimeDeploymentService interface {
	Preview(ctx context.Context, subject platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error)
	Apply(ctx context.Context, subject platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error)
}

type LogRuntimeImageTemplateService interface {
	TemplateValues(ctx context.Context) (map[string]string, error)
}

type LogRuntimeDependencies struct {
	Endpoints             database.LogEndpointStore
	Repository            LogRuntimeRepository
	K8sDeployments        LogRuntimeDeploymentService
	ImageTemplates        LogRuntimeImageTemplateService
	DefaultAlertIngestURL string
	Clock                 func() time.Time
}

type LogRuntimeService struct {
	endpoints             database.LogEndpointStore
	repository            LogRuntimeRepository
	k8sDeployments        LogRuntimeDeploymentService
	imageTemplates        LogRuntimeImageTemplateService
	defaultAlertIngestURL string
	clock                 func() time.Time
}

type LogRuntimePublishRequest struct {
	ClusterID         string `json:"cluster_id"`
	Namespace         string `json:"namespace"`
	AlertIngestURL    string `json:"alert_ingest_url"`
	PreviewID         string `json:"preview_id,omitempty"`
	ConfirmationToken string `json:"confirmation_token,omitempty"`
}

type LogRuntimePublishResult struct {
	RuntimeID            string   `json:"runtime_id"`
	EndpointID           string   `json:"endpoint_id"`
	ClusterID            string   `json:"cluster_id"`
	Namespace            string   `json:"namespace"`
	DatasourceURL        string   `json:"datasource_url"`
	AlertIngestURL       string   `json:"alert_ingest_url"`
	ArtifactHash         string   `json:"artifact_hash"`
	ManifestHash         string   `json:"manifest_hash"`
	ManifestYAML         string   `json:"manifest_yaml"`
	Status               string   `json:"status"`
	Message              string   `json:"message"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
	PreviewID            string   `json:"preview_id,omitempty"`
	ConfirmationToken    string   `json:"confirmation_token,omitempty"`
	AuditID              string   `json:"audit_id,omitempty"`
	AppliedRules         int      `json:"applied_rules"`
	Resources            any      `json:"resources,omitempty"`
	Diffs                any      `json:"diffs,omitempty"`
	Warnings             []string `json:"warnings"`
}

func NewLogRuntimeService(deps LogRuntimeDependencies) LogRuntimeService {
	clock := deps.Clock
	if clock == nil {
		clock = time.Now
	}
	return LogRuntimeService{
		endpoints:             deps.Endpoints,
		repository:            deps.Repository,
		k8sDeployments:        deps.K8sDeployments,
		imageTemplates:        deps.ImageTemplates,
		defaultAlertIngestURL: strings.TrimSpace(deps.DefaultAlertIngestURL),
		clock:                 clock,
	}
}

func (s LogRuntimeService) Publish(ctx context.Context, subject platformrbac.Subject, endpointID string, req LogRuntimePublishRequest) (LogRuntimePublishResult, error) {
	if s.endpoints == nil || s.repository == nil || s.k8sDeployments == nil {
		return LogRuntimePublishResult{}, ErrUnavailable
	}
	endpointID = strings.TrimSpace(endpointID)
	if endpointID == "" {
		return LogRuntimePublishResult{}, apperr.InvalidRequest("日志端点 ID 不能为空")
	}
	var endpoint logs.LogEndpoint
	if err := s.endpoints.FindByID(ctx, endpointID, &endpoint); err != nil {
		return LogRuntimePublishResult{}, mapStoreError(err)
	}
	runtime, err := newLogRuntimeSpec(endpoint, req, s.defaultAlertIngestURL)
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

type logRuntimeSpec struct {
	RuntimeID      string
	Name           string
	EndpointID     string
	ClusterID      string
	Namespace      string
	DatasourceURL  string
	AlertIngestURL string
}

func newLogRuntimeSpec(endpoint logs.LogEndpoint, req LogRuntimePublishRequest, defaultAlertIngestURL string) (logRuntimeSpec, error) {
	endpoint.ID = strings.TrimSpace(endpoint.ID)
	endpoint.Name = strings.TrimSpace(endpoint.Name)
	endpoint.SinkType = strings.TrimSpace(endpoint.SinkType)
	endpoint.ScopeType = strings.TrimSpace(endpoint.ScopeType)
	endpoint.ClusterID = strings.TrimSpace(endpoint.ClusterID)
	endpoint.QueryURL = strings.TrimSpace(endpoint.QueryURL)
	if endpoint.SinkType != logs.EndpointSinkVL {
		return logRuntimeSpec{}, apperr.InvalidRequest("只有 VictoriaLogs 日志端点可以部署 vmalert Runtime")
	}
	if endpoint.ScopeType != logs.EndpointScopeK8sCluster || endpoint.ClusterID == "" {
		return logRuntimeSpec{}, apperr.InvalidRequest("vmalert Runtime 必须绑定 K8s 集群级 VictoriaLogs 端点")
	}
	clusterID := strings.TrimSpace(req.ClusterID)
	if clusterID == "" {
		clusterID = endpoint.ClusterID
	}
	if clusterID != endpoint.ClusterID {
		return logRuntimeSpec{}, apperr.InvalidRequest("vmalert Runtime 必须部署到日志端点绑定的 K8s 集群")
	}
	datasourceURL, err := victoriaLogsDatasourceURL(endpoint.QueryURL)
	if err != nil {
		return logRuntimeSpec{}, err
	}
	alertIngestURL := strings.TrimSpace(req.AlertIngestURL)
	if alertIngestURL == "" {
		alertIngestURL = strings.TrimSpace(defaultAlertIngestURL)
	}
	if alertIngestURL == "" {
		return logRuntimeSpec{}, apperr.InvalidRequest("部署 vmalert Runtime 必须填写 NovaObs Alert Ingest 地址")
	}
	if err := validateHTTPURL(alertIngestURL, "NovaObs Alert Ingest 地址"); err != nil {
		return logRuntimeSpec{}, err
	}
	if namespace := strings.TrimSpace(req.Namespace); namespace != "" && namespace != defaultVmalertNamespace {
		return logRuntimeSpec{}, apperr.InvalidRequest("vmalert Runtime 只能部署到固定 namespace " + defaultVmalertNamespace)
	}
	runtimeID := "vmalert-logs:" + endpoint.ID
	name := dnsName("novaobs-vmalert-" + firstNonEmpty(endpoint.Name, endpoint.ID))
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

func renderLogRuntimeManifest(runtime logRuntimeSpec, artifact Artifact) string {
	rulesBlock := indentBlock(strings.TrimRight(artifact.Content, "\n"), 4)
	return strings.Join([]string{
		"apiVersion: v1",
		"kind: Namespace",
		"metadata:",
		"  name: " + quoteYAML(runtime.Namespace),
		"---",
		"apiVersion: v1",
		"kind: ConfigMap",
		"metadata:",
		"  name: " + quoteYAML(runtime.Name+"-rules"),
		"  namespace: " + quoteYAML(runtime.Namespace),
		"  labels:",
		"    app.kubernetes.io/name: " + quoteYAML(runtime.Name),
		"    app.kubernetes.io/part-of: novaobs",
		"data:",
		"  runtime.yaml: |",
		rulesBlock,
		"---",
		"apiVersion: v1",
		"kind: Service",
		"metadata:",
		"  name: " + quoteYAML(runtime.Name),
		"  namespace: " + quoteYAML(runtime.Namespace),
		"  labels:",
		"    app.kubernetes.io/name: " + quoteYAML(runtime.Name),
		"    app.kubernetes.io/part-of: novaobs",
		"spec:",
		"  type: ClusterIP",
		"  selector:",
		"    app.kubernetes.io/name: " + quoteYAML(runtime.Name),
		"  ports:",
		"    - name: http",
		"      port: 8880",
		"      targetPort: http",
		"---",
		"apiVersion: apps/v1",
		"kind: Deployment",
		"metadata:",
		"  name: " + quoteYAML(runtime.Name),
		"  namespace: " + quoteYAML(runtime.Namespace),
		"  labels:",
		"    app.kubernetes.io/name: " + quoteYAML(runtime.Name),
		"    app.kubernetes.io/part-of: novaobs",
		"spec:",
		"  replicas: 1",
		"  selector:",
		"    matchLabels:",
		"      app.kubernetes.io/name: " + quoteYAML(runtime.Name),
		"  template:",
		"    metadata:",
		"      labels:",
		"        app.kubernetes.io/name: " + quoteYAML(runtime.Name),
		"        app.kubernetes.io/part-of: novaobs",
		"      annotations:",
		"        novaobs.io/runtime-id: " + quoteYAML(runtime.RuntimeID),
		"        novaobs.io/rules-artifact-hash: " + quoteYAML(artifact.Hash),
		"    spec:",
		"      containers:",
		"        - name: vmalert",
		"          image: " + quoteYAML(platformimages.VmalertImagePlaceholder),
		"          args:",
		"            - " + quoteYAML("-rule=/etc/vmalert/rules/*.yaml"),
		"            - " + quoteYAML("-rule.defaultRuleType=vlogs"),
		"            - " + quoteYAML("-configCheckInterval=10s"),
		"            - " + quoteYAML("-datasource.url="+runtime.DatasourceURL),
		"            - " + quoteYAML("-notifier.url="+runtime.AlertIngestURL),
		"            - " + quoteYAML("-httpListenAddr=:8880"),
		"            - " + quoteYAML("-external.label=novaobs_runtime_id="+runtime.RuntimeID),
		"          ports:",
		"            - name: http",
		"              containerPort: 8880",
		"          volumeMounts:",
		"            - name: rules",
		"              mountPath: /etc/vmalert/rules",
		"              readOnly: true",
		"      volumes:",
		"        - name: rules",
		"          configMap:",
		"            name: " + quoteYAML(runtime.Name+"-rules"),
		"",
	}, "\n")
}

func victoriaLogsDatasourceURL(queryURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(queryURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", apperr.InvalidRequest("VictoriaLogs 查询地址必须是完整的 http/https 地址")
	}
	path := strings.TrimRight(parsed.Path, "/")
	for _, suffix := range []string{"/select/logsql/query", "/select/logsql", "/select/vmui"} {
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

func validateHTTPURL(raw string, label string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return apperr.InvalidRequest(label + "必须是完整的 http/https 地址")
	}
	return nil
}

func dnsName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	re := regexp.MustCompile(`[^a-z0-9-]+`)
	value = re.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		value = "novaobs-vmalert"
	}
	if len(value) > 63 {
		value = strings.TrimRight(value[:52], "-") + "-" + hashText(value)[:10]
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func quoteYAML(value string) string {
	out, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%q", value)
	}
	return strings.TrimSpace(string(out))
}

func indentBlock(value string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	if strings.TrimSpace(value) == "" {
		return prefix + "groups: []"
	}
	lines := strings.Split(value, "\n")
	for index := range lines {
		lines[index] = prefix + lines[index]
	}
	return strings.Join(lines, "\n")
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
