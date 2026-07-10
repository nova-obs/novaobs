package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"novaapm/internal/database"
	"novaapm/internal/logs"
	platformaudit "novaapm/internal/platform/audit"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/pkg/apperr"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type Service struct {
	logEndpoints database.LogEndpointStore
	authorizer   Authorizer
	auditor      Auditor
	httpClient   HTTPDoer
	now          func() time.Time
}

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type Auditor interface {
	Record(ctx context.Context, event platformaudit.Event) (platformaudit.Event, error)
}

type HTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

type ServiceOption func(*Service)

func WithAuthorizer(authorizer Authorizer) ServiceOption {
	return func(s *Service) {
		if authorizer != nil {
			s.authorizer = authorizer
		}
	}
}

func WithAuditor(auditor Auditor) ServiceOption {
	return func(s *Service) {
		if auditor != nil {
			s.auditor = auditor
		}
	}
}

func WithHTTPClient(client HTTPDoer) ServiceOption {
	return func(s *Service) {
		if client != nil {
			s.httpClient = client
		}
	}
}

func NewLogEndpointFacade(logEndpoints database.LogEndpointStore, options ...ServiceOption) Service {
	service := Service{
		logEndpoints: logEndpoints,
		now:          time.Now,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	for _, option := range options {
		if option != nil {
			option(&service)
		}
	}
	return service
}

func (s Service) CreateForSubject(ctx context.Context, subject platformrbac.Subject, endpoint Endpoint) (Endpoint, error) {
	if !s.allowedUnifiedManage(subject) {
		return Endpoint{}, ErrPermissionDenied
	}
	created, err := s.Create(ctx, endpoint)
	if err != nil {
		return Endpoint{}, err
	}
	if err := s.recordMutation(ctx, subject, "create", created); err != nil {
		return Endpoint{}, err
	}
	return created, nil
}

func (s Service) UpdateForSubject(ctx context.Context, subject platformrbac.Subject, id string, endpoint Endpoint) (Endpoint, error) {
	if !s.allowedUnifiedManage(subject) {
		return Endpoint{}, ErrPermissionDenied
	}
	updated, err := s.Update(ctx, id, endpoint)
	if err != nil {
		return Endpoint{}, err
	}
	if err := s.recordMutation(ctx, subject, "update", updated); err != nil {
		return Endpoint{}, err
	}
	return updated, nil
}

func (s Service) Create(ctx context.Context, endpoint Endpoint) (Endpoint, error) {
	if s.logEndpoints == nil {
		return Endpoint{}, apperr.InvalidRequest("观测端点存储不可用")
	}
	endpoint = normalizeManagedEndpoint(endpoint)
	if err := validateManagedEndpoint(endpoint); err != nil {
		return Endpoint{}, err
	}
	existing, err := s.List(ctx, ListFilter{})
	if err != nil {
		return Endpoint{}, err
	}
	for _, item := range existing {
		if strings.EqualFold(item.Name, endpoint.Name) {
			return Endpoint{}, apperr.Conflict("观测端点名称已存在")
		}
	}
	endpoint.ID = primitive.NewObjectID().Hex()
	now := s.now().UTC()
	endpoint.Health = EndpointHealth{Status: HealthStatusUnknown}
	endpoint.CreatedAt = now
	endpoint.UpdatedAt = now
	if err := s.logEndpoints.Insert(ctx, mapEndpointToLogEndpoint(endpoint)); err != nil {
		if errors.Is(err, database.ErrConflict) {
			return Endpoint{}, apperr.Conflict("观测端点已存在")
		}
		return Endpoint{}, err
	}
	return endpoint, nil
}

func (s Service) Update(ctx context.Context, id string, endpoint Endpoint) (Endpoint, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Endpoint{}, apperr.InvalidRequest("观测端点 ID 不能为空")
	}
	current, err := s.Get(ctx, id)
	if err != nil {
		return Endpoint{}, err
	}
	endpoint = normalizeManagedEndpoint(endpoint)
	if endpoint.Kind != current.Kind || !sameStringSet(endpoint.SignalTypes, current.SignalTypes) {
		return Endpoint{}, apperr.InvalidRequest("观测端点创建后不能变更 kind 或 signal_types")
	}
	if err := validateManagedEndpoint(endpoint); err != nil {
		return Endpoint{}, err
	}
	all, err := s.List(ctx, ListFilter{})
	if err != nil {
		return Endpoint{}, err
	}
	for _, item := range all {
		if item.ID != id && strings.EqualFold(item.Name, endpoint.Name) {
			return Endpoint{}, apperr.Conflict("观测端点名称已存在")
		}
	}
	endpoint.ID = id
	endpoint.Health = current.Health
	endpoint.CreatedAt = current.CreatedAt
	endpoint.UpdatedAt = s.now().UTC()
	if err := s.logEndpoints.Update(ctx, id, mapEndpointToLogEndpoint(endpoint)); err != nil {
		return Endpoint{}, normalizeNotFound(err, "观测端点不存在")
	}
	return endpoint, nil
}

func (s Service) List(ctx context.Context, filter ListFilter) ([]Endpoint, error) {
	if s.logEndpoints == nil {
		return []Endpoint{}, nil
	}
	var stored []logs.LogEndpoint
	if err := s.logEndpoints.FindAll(ctx, &stored); err != nil {
		return nil, err
	}
	filter = normalizeFilter(filter)
	out := make([]Endpoint, 0, len(stored))
	for _, item := range stored {
		endpoint := mapLogEndpoint(item)
		if filter.Kind != "" && endpoint.Kind != filter.Kind {
			continue
		}
		if filter.SignalType != "" && !containsSignalType(endpoint.SignalTypes, filter.SignalType) {
			continue
		}
		out = append(out, endpoint)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s Service) ListForSubject(ctx context.Context, subject platformrbac.Subject, filter ListFilter) ([]Endpoint, error) {
	if !s.allowed(subject, filter, "read") {
		return nil, ErrPermissionDenied
	}
	return s.List(ctx, filter)
}

func (s Service) Get(ctx context.Context, id string) (Endpoint, error) {
	if s.logEndpoints == nil {
		return Endpoint{}, apperr.NotFound("观测端点不存在")
	}
	var stored logs.LogEndpoint
	if err := s.logEndpoints.FindByID(ctx, strings.TrimSpace(id), &stored); err != nil {
		return Endpoint{}, normalizeNotFound(err, "观测端点不存在")
	}
	return mapLogEndpoint(stored), nil
}

func (s Service) TestForSubject(ctx context.Context, subject platformrbac.Subject, id string) (TestResult, error) {
	if !s.allowedUnifiedManage(subject) {
		return TestResult{}, ErrPermissionDenied
	}
	endpoint, err := s.Get(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	result := s.testEndpoint(ctx, endpoint)
	if err := s.persistHealth(ctx, endpoint.ID, result); err != nil {
		return TestResult{}, err
	}
	return result, nil
}

func (s Service) Test(ctx context.Context, id string) (TestResult, error) {
	endpoint, err := s.Get(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	result := s.testEndpoint(ctx, endpoint)
	if err := s.persistHealth(ctx, endpoint.ID, result); err != nil {
		return TestResult{}, err
	}
	return result, nil
}

func (s Service) testEndpoint(ctx context.Context, endpoint Endpoint) TestResult {
	now := s.now().UTC()
	if strings.TrimSpace(endpoint.Status) == "disabled" {
		return TestResult{
			Status:    HealthStatusFailed,
			Message:   "观测端点已停用",
			CheckedAt: now,
		}
	}
	if !endpointHasAnyURL(endpoint) {
		return TestResult{
			Status:    HealthStatusFailed,
			Message:   "观测端点缺少查询、VMUI 或写入地址",
			CheckedAt: now,
		}
	}
	if endpoint.Kind == KindVictoriaMetrics && containsSignalType(endpoint.SignalTypes, SignalTypeMetrics) {
		return s.testVictoriaMetrics(ctx, endpoint)
	}
	return TestResult{
		Status:    HealthStatusPending,
		Message:   "观测端点配置完整，真实连接测试将在后续运行时执行",
		CheckedAt: now,
	}
}

func (s Service) testVictoriaMetrics(ctx context.Context, endpoint Endpoint) TestResult {
	checkedAt := s.now().UTC()
	probeURL, err := buildVictoriaMetricsProbeURL(endpoint.URLs.QueryURL)
	if err != nil {
		return TestResult{Status: HealthStatusFailed, Message: err.Error(), CheckedAt: checkedAt}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return TestResult{Status: HealthStatusFailed, Message: "VictoriaMetrics 探测请求无效", CheckedAt: checkedAt}
	}
	startedAt := time.Now()
	response, err := s.httpClient.Do(request)
	responseTimeMS := int(time.Since(startedAt).Milliseconds())
	if err != nil {
		return TestResult{Status: HealthStatusFailed, Message: "VictoriaMetrics 连接失败", ResponseTimeMS: responseTimeMS, CheckedAt: checkedAt}
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return TestResult{Status: HealthStatusFailed, Message: fmt.Sprintf("VictoriaMetrics 返回 HTTP %d", response.StatusCode), ResponseTimeMS: responseTimeMS, CheckedAt: checkedAt}
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body); err != nil || body.Status != "success" {
		return TestResult{Status: HealthStatusFailed, Message: "VictoriaMetrics 查询响应无效", ResponseTimeMS: responseTimeMS, CheckedAt: checkedAt}
	}
	return TestResult{Status: "healthy", Message: "VictoriaMetrics 查询端点连通", ResponseTimeMS: responseTimeMS, CheckedAt: checkedAt}
}

func (s Service) persistHealth(ctx context.Context, endpointID string, result TestResult) error {
	if s.logEndpoints == nil {
		return apperr.InvalidRequest("观测端点存储不可用")
	}
	var stored logs.LogEndpoint
	if err := s.logEndpoints.FindByID(ctx, endpointID, &stored); err != nil {
		return normalizeNotFound(err, "观测端点不存在")
	}
	stored.Health = logs.LogEndpointHealth{
		Status:         result.Status,
		CheckedAt:      result.CheckedAt,
		ResponseTimeMS: result.ResponseTimeMS,
		Message:        result.Message,
	}
	return s.logEndpoints.Update(ctx, endpointID, stored)
}

func mapLogEndpoint(item logs.LogEndpoint) Endpoint {
	kind := strings.ToLower(strings.TrimSpace(item.Kind))
	signals := normalizeSignalTypes(item.SignalTypes)
	if kind == "" || len(signals) == 0 {
		defaultKind, defaultSignals := defaultsForLogEndpoint(item)
		if kind == "" {
			kind = defaultKind
		}
		if len(signals) == 0 {
			signals = defaultSignals
		}
	}
	scopeType := strings.TrimSpace(item.ScopeType)
	if scopeType == "" {
		if strings.TrimSpace(item.ClusterID) != "" {
			scopeType = logs.EndpointScopeK8sCluster
		} else {
			scopeType = logs.EndpointScopeGlobal
		}
	}
	status := strings.TrimSpace(item.Status)
	if status == "" {
		status = "active"
	}
	return Endpoint{
		ID:          strings.TrimSpace(item.ID),
		Name:        strings.TrimSpace(item.Name),
		Description: strings.TrimSpace(item.Description),
		Kind:        kind,
		SignalTypes: signals,
		Scope: EndpointScope{
			Type:      scopeType,
			ClusterID: strings.TrimSpace(item.ClusterID),
		},
		URLs: EndpointURLs{
			WriteURL:       strings.TrimSpace(item.WriteURL),
			RemoteWriteURL: strings.TrimSpace(item.WriteURL),
			QueryURL:       strings.TrimSpace(item.QueryURL),
			UIURL:          strings.TrimSpace(item.VMUIURL),
		},
		Tenant: EndpointTenant{
			AccountID: strings.TrimSpace(item.AccountID),
			ProjectID: strings.TrimSpace(item.ProjectID),
		},
		SecretRef: strings.TrimSpace(item.SecretRef),
		Status:    status,
		Health: EndpointHealth{
			Status:         firstNonEmpty(strings.TrimSpace(item.Health.Status), HealthStatusUnknown),
			CheckedAt:      item.Health.CheckedAt,
			ResponseTimeMS: item.Health.ResponseTimeMS,
			Message:        strings.TrimSpace(item.Health.Message),
		},
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}
}

func mapEndpointToLogEndpoint(endpoint Endpoint) logs.LogEndpoint {
	return logs.LogEndpoint{
		ID:          endpoint.ID,
		Name:        endpoint.Name,
		Description: endpoint.Description,
		Kind:        endpoint.Kind,
		SignalTypes: append([]string{}, endpoint.SignalTypes...),
		WriteURL:    firstNonEmpty(endpoint.URLs.RemoteWriteURL, endpoint.URLs.WriteURL),
		QueryURL:    endpoint.URLs.QueryURL,
		VMUIURL:     endpoint.URLs.UIURL,
		ScopeType:   endpoint.Scope.Type,
		ClusterID:   endpoint.Scope.ClusterID,
		SecretRef:   endpoint.SecretRef,
		Status:      endpoint.Status,
		Health: logs.LogEndpointHealth{
			Status:         endpoint.Health.Status,
			CheckedAt:      endpoint.Health.CheckedAt,
			ResponseTimeMS: endpoint.Health.ResponseTimeMS,
			Message:        endpoint.Health.Message,
		},
		CreatedAt: endpoint.CreatedAt,
		UpdatedAt: endpoint.UpdatedAt,
	}
}

func defaultsForLogEndpoint(item logs.LogEndpoint) (string, []string) {
	switch strings.ToLower(strings.TrimSpace(item.SinkType)) {
	case logs.EndpointSinkES:
		return KindElasticsearch, []string{SignalTypeLogs}
	case logs.EndpointSinkKafka:
		return KindKafka, []string{SignalTypeLogs}
	case logs.EndpointSinkOTel:
		return KindOTel, []string{SignalTypeLogs}
	default:
		return KindVictoriaLogs, []string{SignalTypeLogs}
	}
}

func normalizeFilter(filter ListFilter) ListFilter {
	filter.SignalType = strings.ToLower(strings.TrimSpace(filter.SignalType))
	filter.Kind = strings.ToLower(strings.TrimSpace(filter.Kind))
	return filter
}

func normalizeSignalTypes(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func ContainsSignalType(endpoint Endpoint, signalType string) bool {
	return containsSignalType(endpoint.SignalTypes, strings.ToLower(strings.TrimSpace(signalType)))
}

func containsSignalType(values []string, signalType string) bool {
	for _, value := range values {
		if value == signalType {
			return true
		}
	}
	return false
}

func endpointHasAnyURL(endpoint Endpoint) bool {
	return strings.TrimSpace(endpoint.URLs.QueryURL) != "" ||
		strings.TrimSpace(endpoint.URLs.UIURL) != "" ||
		strings.TrimSpace(endpoint.URLs.WriteURL) != "" ||
		strings.TrimSpace(endpoint.URLs.RemoteWriteURL) != "" ||
		strings.TrimSpace(endpoint.URLs.BaseURL) != ""
}

func (s Service) allowed(subject platformrbac.Subject, filter ListFilter, action string) bool {
	if subject.ID == "" || subject.Type == "" || s.authorizer == nil {
		return false
	}
	resource := "observability.endpoint"
	switch strings.ToLower(strings.TrimSpace(filter.SignalType)) {
	case SignalTypeMetrics:
		resource = "metrics.endpoint"
	case SignalTypeLogs:
		resource = "logs.target"
	}
	decision := s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: resource,
		Action:   action,
		Scope:    platformrbac.Scope{},
	})
	return decision.Allowed
}

func normalizeNotFound(err error, message string) error {
	if errors.Is(err, mongo.ErrNoDocuments) || errors.Is(err, database.ErrNotFound) {
		return apperr.NotFound(message)
	}
	return err
}

func normalizeManagedEndpoint(endpoint Endpoint) Endpoint {
	endpoint.Name = strings.TrimSpace(endpoint.Name)
	endpoint.Description = strings.TrimSpace(endpoint.Description)
	endpoint.Kind = strings.ToLower(strings.TrimSpace(endpoint.Kind))
	endpoint.SignalTypes = normalizeSignalTypes(endpoint.SignalTypes)
	endpoint.Scope.Type = strings.ToLower(strings.TrimSpace(endpoint.Scope.Type))
	endpoint.Scope.ClusterID = strings.TrimSpace(endpoint.Scope.ClusterID)
	endpoint.Scope.Namespace = strings.TrimSpace(endpoint.Scope.Namespace)
	endpoint.URLs.WriteURL = strings.TrimSpace(endpoint.URLs.WriteURL)
	endpoint.URLs.RemoteWriteURL = strings.TrimSpace(endpoint.URLs.RemoteWriteURL)
	endpoint.URLs.QueryURL = strings.TrimSpace(endpoint.URLs.QueryURL)
	endpoint.URLs.UIURL = strings.TrimSpace(endpoint.URLs.UIURL)
	endpoint.URLs.BaseURL = strings.TrimSpace(endpoint.URLs.BaseURL)
	endpoint.SecretRef = strings.TrimSpace(endpoint.SecretRef)
	endpoint.Status = strings.ToLower(strings.TrimSpace(endpoint.Status))
	if endpoint.Scope.Type == "" {
		endpoint.Scope.Type = "global"
	}
	if endpoint.Status == "" {
		endpoint.Status = "active"
	}
	if endpoint.URLs.RemoteWriteURL == "" {
		endpoint.URLs.RemoteWriteURL = endpoint.URLs.WriteURL
	}
	if endpoint.URLs.WriteURL == "" {
		endpoint.URLs.WriteURL = endpoint.URLs.RemoteWriteURL
	}
	// 租户始终由产品派生，物理端点不保存业务租户。
	endpoint.Tenant = EndpointTenant{}
	return endpoint
}

func validateManagedEndpoint(endpoint Endpoint) error {
	if endpoint.Name == "" || len(endpoint.Name) > 120 {
		return apperr.InvalidRequest("观测端点名称不能为空且不能超过 120 个字符")
	}
	if len(endpoint.Description) > 2048 {
		return apperr.InvalidRequest("观测端点说明不能超过 2048 个字符")
	}
	if endpoint.Kind != KindVictoriaMetrics {
		return apperr.InvalidRequest("统一观测端点登记当前只支持 VictoriaMetrics")
	}
	if len(endpoint.SignalTypes) != 1 || endpoint.SignalTypes[0] != SignalTypeMetrics {
		return apperr.InvalidRequest("VictoriaMetrics 端点 signal_types 必须为 metrics")
	}
	if endpoint.Status != "active" && endpoint.Status != "disabled" {
		return apperr.InvalidRequest("观测端点状态只支持 active 或 disabled")
	}
	if endpoint.Scope.Type != "global" && endpoint.Scope.Type != "k8s_cluster" {
		return apperr.InvalidRequest("VictoriaMetrics 端点作用域只支持 global 或 k8s_cluster")
	}
	if endpoint.Scope.Type == "k8s_cluster" && endpoint.Scope.ClusterID == "" {
		return apperr.InvalidRequest("K8s 集群级 VictoriaMetrics 端点必须填写 cluster_id")
	}
	if err := validateManagedURL(endpoint.URLs.RemoteWriteURL, "VictoriaMetrics Remote Write 地址"); err != nil {
		return err
	}
	if err := validateManagedURL(endpoint.URLs.QueryURL, "VictoriaMetrics 查询地址"); err != nil {
		return err
	}
	if err := validateManagedURL(endpoint.URLs.UIURL, "VictoriaMetrics VMUI 地址"); err != nil {
		return err
	}
	if !strings.Contains(endpointURLPath(endpoint.URLs.RemoteWriteURL), "/insert/0/prometheus") {
		return apperr.InvalidRequest("VictoriaMetrics Remote Write 地址必须包含 /insert/0/prometheus 租户占位路径")
	}
	if !strings.Contains(endpointURLPath(endpoint.URLs.QueryURL), "/select/0/prometheus") {
		return apperr.InvalidRequest("VictoriaMetrics 查询地址必须包含 /select/0/prometheus 租户占位路径")
	}
	if !strings.Contains(endpointURLPath(endpoint.URLs.UIURL), "/select/0/vmui") {
		return apperr.InvalidRequest("VictoriaMetrics VMUI 地址必须包含 /select/0/vmui 租户占位路径")
	}
	return nil
}

func validateManagedURL(rawURL string, label string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return apperr.InvalidRequest(label + "必须是完整的 http/https 地址")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return apperr.InvalidRequest(label + "只支持 http/https")
	}
	if parsed.User != nil {
		return apperr.InvalidRequest(label + "不能内嵌用户名或密码，请使用 secret_ref")
	}
	if parsed.Fragment != "" {
		return apperr.InvalidRequest(label + "不能包含 fragment")
	}
	return nil
}

func endpointURLPath(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(parsed.Path, "/")
}

func buildVictoriaMetricsProbeURL(rawURL string) (string, error) {
	if err := validateManagedURL(rawURL, "VictoriaMetrics 查询地址"); err != nil {
		return "", err
	}
	parsed, _ := url.Parse(rawURL)
	path := strings.TrimSuffix(parsed.Path, "/")
	if !strings.HasSuffix(path, "/api/v1/query") {
		path += "/api/v1/query"
	}
	parsed.Path = path
	query := parsed.Query()
	query.Set("query", "vector(1)")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func sameStringSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, item := range left {
		seen[item]++
	}
	for _, item := range right {
		if seen[item] == 0 {
			return false
		}
		seen[item]--
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (s Service) allowedUnifiedManage(subject platformrbac.Subject) bool {
	if subject.ID == "" || subject.Type == "" || s.authorizer == nil {
		return false
	}
	return s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "observability.endpoint",
		Action:   "manage",
		Scope:    platformrbac.Scope{},
	}).Allowed
}

func (s Service) recordMutation(ctx context.Context, subject platformrbac.Subject, action string, endpoint Endpoint) error {
	if s.auditor == nil {
		return nil
	}
	_, err := s.auditor.Record(ctx, platformaudit.Event{
		Actor:    platformaudit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource: platformaudit.Resource{Type: "observability_endpoint", Name: endpoint.ID},
		Action:   action,
		Scope:    endpoint.Scope.Type,
		RequestSummary: map[string]any{
			"kind": endpoint.Kind, "signal_types": append([]string{}, endpoint.SignalTypes...), "scope_type": endpoint.Scope.Type, "cluster_id": endpoint.Scope.ClusterID, "status": endpoint.Status,
		},
		Result: "success",
	})
	return err
}
