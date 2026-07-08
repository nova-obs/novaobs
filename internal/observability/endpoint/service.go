package endpoint

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"novaobs/internal/database"
	"novaobs/internal/logs"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/pkg/apperr"

	"go.mongodb.org/mongo-driver/mongo"
)

type Service struct {
	logEndpoints database.LogEndpointStore
	authorizer   Authorizer
	now          func() time.Time
}

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type ServiceOption func(*Service)

func WithAuthorizer(authorizer Authorizer) ServiceOption {
	return func(s *Service) {
		if authorizer != nil {
			s.authorizer = authorizer
		}
	}
}

func NewLogEndpointFacade(logEndpoints database.LogEndpointStore, options ...ServiceOption) Service {
	service := Service{logEndpoints: logEndpoints, now: time.Now}
	for _, option := range options {
		if option != nil {
			option(&service)
		}
	}
	return service
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
	endpoint, err := s.Get(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	filter := ListFilter{}
	if containsSignalType(endpoint.SignalTypes, SignalTypeMetrics) {
		filter.SignalType = SignalTypeMetrics
	} else if containsSignalType(endpoint.SignalTypes, SignalTypeLogs) {
		filter.SignalType = SignalTypeLogs
	}
	if !s.allowed(subject, filter, "read") {
		return TestResult{}, ErrPermissionDenied
	}
	return s.testEndpoint(endpoint), nil
}

func (s Service) Test(ctx context.Context, id string) (TestResult, error) {
	endpoint, err := s.Get(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	return s.testEndpoint(endpoint), nil
}

func (s Service) testEndpoint(endpoint Endpoint) TestResult {
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
	return TestResult{
		Status:    HealthStatusPending,
		Message:   "观测端点配置完整，真实连接测试将在后续运行时执行",
		CheckedAt: now,
	}
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
			WriteURL: strings.TrimSpace(item.WriteURL),
			QueryURL: strings.TrimSpace(item.QueryURL),
			UIURL:    strings.TrimSpace(item.VMUIURL),
		},
		Tenant: EndpointTenant{
			AccountID: strings.TrimSpace(item.AccountID),
			ProjectID: strings.TrimSpace(item.ProjectID),
		},
		SecretRef: strings.TrimSpace(item.SecretRef),
		Status:    status,
		Health: EndpointHealth{
			Status: HealthStatusUnknown,
		},
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
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
