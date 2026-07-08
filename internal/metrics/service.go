package metrics

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"novaobs/internal/database"
	obsendpoint "novaobs/internal/observability/endpoint"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/internal/servicecatalog"
	"novaobs/pkg/apperr"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

var ErrPermissionDenied = errors.New("permission_denied")

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type EndpointReader interface {
	List(ctx context.Context, filter obsendpoint.ListFilter) ([]obsendpoint.Endpoint, error)
	Get(ctx context.Context, id string) (obsendpoint.Endpoint, error)
	Test(ctx context.Context, id string) (obsendpoint.TestResult, error)
}

type Dependencies struct {
	Bindings   database.MetricsServiceBindingStore
	Endpoints  EndpointReader
	Services   servicecatalog.Repository
	Authorizer Authorizer
}

type Service struct {
	bindings   database.MetricsServiceBindingStore
	endpoints  EndpointReader
	services   servicecatalog.Repository
	authorizer Authorizer
	now        func() time.Time
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(platformrbac.Subject, platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

func NewService(deps Dependencies) Service {
	authorizer := deps.Authorizer
	if authorizer == nil {
		authorizer = denyAuthorizer{}
	}
	return Service{
		bindings:   deps.Bindings,
		endpoints:  deps.Endpoints,
		services:   deps.Services,
		authorizer: authorizer,
		now:        time.Now,
	}
}

func (s Service) ListEndpoints(ctx context.Context, subject platformrbac.Subject) ([]obsendpoint.Endpoint, error) {
	if !s.allowed(subject, "", "metrics.endpoint", "read") {
		return nil, ErrPermissionDenied
	}
	if s.endpoints == nil {
		return []obsendpoint.Endpoint{}, nil
	}
	return s.endpoints.List(ctx, obsendpoint.ListFilter{SignalType: obsendpoint.SignalTypeMetrics})
}

func (s Service) TestEndpoint(ctx context.Context, subject platformrbac.Subject, endpointID string) (obsendpoint.TestResult, error) {
	if !s.allowed(subject, "", "metrics.endpoint", "read") {
		return obsendpoint.TestResult{}, ErrPermissionDenied
	}
	if s.endpoints == nil {
		return obsendpoint.TestResult{}, apperr.NotFound("指标端点不存在")
	}
	return s.endpoints.Test(ctx, endpointID)
}

func (s Service) ListBindings(ctx context.Context, subject platformrbac.Subject, serviceID string) ([]ServiceBindingView, error) {
	if s.bindings == nil {
		return []ServiceBindingView{}, nil
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID != "" && !s.allowed(subject, serviceID, "metrics.binding", "read") {
		return nil, ErrPermissionDenied
	}
	bindings, err := s.findBindings(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	views := make([]ServiceBindingView, 0, len(bindings))
	for _, binding := range bindings {
		if serviceID == "" && !s.allowed(subject, binding.ServiceID, "metrics.binding", "read") {
			continue
		}
		view, err := s.bindingView(ctx, normalizeBinding(binding))
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (s Service) CreateBinding(ctx context.Context, subject platformrbac.Subject, req CreateServiceBindingRequest) (ServiceBindingView, error) {
	if s.bindings == nil {
		return ServiceBindingView{}, apperr.InvalidRequest("指标服务绑定存储不可用")
	}
	req = normalizeCreateRequest(req)
	if req.ServiceID == "" || req.EndpointID == "" {
		return ServiceBindingView{}, apperr.InvalidRequest("service_id 和 endpoint_id 不能为空")
	}
	if len(req.LabelMatch) == 0 {
		return ServiceBindingView{}, apperr.InvalidRequest("label_match 不能为空")
	}
	if err := validateLabelMatch(req.LabelMatch); err != nil {
		return ServiceBindingView{}, err
	}
	service, err := s.services.Get(ctx, req.ServiceID)
	if err != nil {
		return ServiceBindingView{}, normalizeNotFound(err, "服务不存在")
	}
	if !s.allowed(subject, service.ID, "metrics.binding", "manage") {
		return ServiceBindingView{}, ErrPermissionDenied
	}
	endpoint, err := s.metricsEndpoint(ctx, req.EndpointID)
	if err != nil {
		return ServiceBindingView{}, err
	}
	status := firstNonEmpty(req.Status, BindingStatusActive)
	if !validBindingStatus(status) {
		return ServiceBindingView{}, apperr.InvalidRequest("指标服务绑定状态只支持 active、pending_verification 或 disabled")
	}
	if status == BindingStatusActive {
		if err := validateMetricQueryScope(req.BasePromQL, req.LabelMatch); err != nil {
			return ServiceBindingView{}, err
		}
		if err := s.ensureNoOtherActiveBinding(ctx, "", service.ID); err != nil {
			return ServiceBindingView{}, err
		}
	}
	basePromQL := MetricScopePromQL(req.BasePromQL, req.LabelMatch)
	now := s.now().UTC()
	actor := actorRefFromSubject(subject)
	binding := ServiceBinding{
		ID:               primitive.NewObjectID().Hex(),
		ServiceID:        service.ID,
		EndpointID:       endpoint.ID,
		Tenant:           firstTenant(req.Tenant, endpoint.Tenant),
		LabelMatch:       copyLabels(req.LabelMatch),
		BasePromQL:       basePromQL,
		Status:           status,
		LastProbeStatus:  ProbeStatusPending,
		LastProbeMessage: "等待配置验证",
		CreatedBy:        actor,
		UpdatedBy:        actor,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.bindings.Insert(ctx, binding); err != nil {
		if errors.Is(err, database.ErrConflict) {
			return ServiceBindingView{}, apperr.Conflict("服务已存在 active metrics binding")
		}
		return ServiceBindingView{}, err
	}
	return s.bindingView(ctx, binding)
}

func (s Service) UpdateBinding(ctx context.Context, subject platformrbac.Subject, id string, req UpdateServiceBindingRequest) (ServiceBindingView, error) {
	current, err := s.getBinding(ctx, id)
	if err != nil {
		return ServiceBindingView{}, err
	}
	if !s.allowed(subject, current.ServiceID, "metrics.binding", "manage") {
		return ServiceBindingView{}, ErrPermissionDenied
	}
	req = normalizeUpdateRequest(req)
	updated := current
	if req.EndpointID != "" {
		endpoint, err := s.metricsEndpoint(ctx, req.EndpointID)
		if err != nil {
			return ServiceBindingView{}, err
		}
		updated.EndpointID = endpoint.ID
		if req.Tenant.AccountID == "" && req.Tenant.ProjectID == "" {
			updated.Tenant = endpoint.Tenant
		}
	}
	if req.Tenant.AccountID != "" || req.Tenant.ProjectID != "" {
		updated.Tenant = req.Tenant
	}
	if req.LabelMatch != nil {
		if len(req.LabelMatch) == 0 {
			return ServiceBindingView{}, apperr.InvalidRequest("label_match 不能为空")
		}
		if err := validateLabelMatch(req.LabelMatch); err != nil {
			return ServiceBindingView{}, err
		}
		updated.LabelMatch = copyLabels(req.LabelMatch)
	}
	if req.BasePromQL != nil {
		updated.BasePromQL = strings.TrimSpace(*req.BasePromQL)
	}
	if req.Status != "" {
		if !validBindingStatus(req.Status) {
			return ServiceBindingView{}, apperr.InvalidRequest("指标服务绑定状态只支持 active、pending_verification 或 disabled")
		}
		updated.Status = req.Status
	}
	updated.BasePromQL = MetricScopePromQL(updated.BasePromQL, updated.LabelMatch)
	if updated.Status == BindingStatusActive {
		if err := validateMetricQueryScope(updated.BasePromQL, updated.LabelMatch); err != nil {
			return ServiceBindingView{}, err
		}
		if err := s.ensureNoOtherActiveBinding(ctx, updated.ID, updated.ServiceID); err != nil {
			return ServiceBindingView{}, err
		}
	}
	updated.UpdatedBy = actorRefFromSubject(subject)
	updated.UpdatedAt = s.now().UTC()
	if err := s.bindings.Update(ctx, updated.ID, updated); err != nil {
		if errors.Is(err, database.ErrConflict) {
			return ServiceBindingView{}, apperr.Conflict("服务已存在 active metrics binding")
		}
		return ServiceBindingView{}, normalizeNotFound(err, "指标服务绑定不存在")
	}
	return s.bindingView(ctx, updated)
}

func (s Service) ProbeBinding(ctx context.Context, subject platformrbac.Subject, id string) (ServiceBindingView, error) {
	current, err := s.getBinding(ctx, id)
	if err != nil {
		return ServiceBindingView{}, err
	}
	if !s.allowed(subject, current.ServiceID, "metrics.binding", "manage") {
		return ServiceBindingView{}, ErrPermissionDenied
	}
	_, err = s.services.Get(ctx, current.ServiceID)
	if err != nil {
		return ServiceBindingView{}, normalizeNotFound(err, "服务不存在")
	}
	endpoint, err := s.metricsEndpoint(ctx, current.EndpointID)
	if err != nil {
		return ServiceBindingView{}, err
	}
	status, message := ProbeStatusVerified, "指标服务绑定配置完整，等待真实样本查询验证"
	if len(current.LabelMatch) == 0 {
		status, message = ProbeStatusFailed, "label_match 不能为空"
	} else if err := validateMetricQueryScope(current.BasePromQL, current.LabelMatch); err != nil {
		status, message = ProbeStatusFailed, err.Error()
	} else if !endpointHasAnyURL(endpoint) {
		status, message = ProbeStatusFailed, "指标端点缺少查询、VMUI 或写入地址"
	}
	now := s.now().UTC()
	current.LastProbeStatus = status
	current.LastProbeMessage = message
	current.LastProbeAt = &now
	current.UpdatedBy = actorRefFromSubject(subject)
	current.UpdatedAt = now
	if err := s.bindings.Update(ctx, current.ID, current); err != nil {
		return ServiceBindingView{}, normalizeNotFound(err, "指标服务绑定不存在")
	}
	return s.bindingView(ctx, current)
}

func (s Service) Workspace(ctx context.Context, subject platformrbac.Subject, serviceID string) (Workspace, error) {
	services, err := s.services.List(ctx)
	if err != nil {
		return Workspace{}, err
	}
	services = s.filterReadableServices(subject, services)
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		for _, service := range services {
			serviceID = service.ID
			break
		}
	}
	if serviceID != "" {
		if _, err := s.services.Get(ctx, serviceID); err != nil {
			return Workspace{}, normalizeNotFound(err, "服务不存在")
		}
		if !s.allowed(subject, serviceID, "metrics.query", "read") {
			return Workspace{}, ErrPermissionDenied
		}
	}
	endpoints := []obsendpoint.Endpoint{}
	if s.endpoints != nil && s.allowed(subject, "", "metrics.endpoint", "read") {
		endpoints, err = s.endpoints.List(ctx, obsendpoint.ListFilter{SignalType: obsendpoint.SignalTypeMetrics})
		if err != nil {
			return Workspace{}, err
		}
	}
	var bindingView *ServiceBindingView
	if serviceID != "" && s.bindings != nil && s.allowed(subject, serviceID, "metrics.binding", "read") {
		bindings, err := s.findBindings(ctx, serviceID)
		if err != nil {
			return Workspace{}, err
		}
		for _, binding := range bindings {
			binding = normalizeBinding(binding)
			if binding.Status == BindingStatusActive {
				view, err := s.bindingView(ctx, binding)
				if err != nil {
					return Workspace{}, err
				}
				copy := view
				bindingView = &copy
				break
			}
		}
	}
	return Workspace{
		Services:        services,
		ActiveServiceID: serviceID,
		Binding:         bindingView,
		Endpoints:       endpoints,
		CollectorGroups: []any{},
		AlertRules:      []any{},
		Dashboards:      []any{},
	}, nil
}

func (s Service) filterReadableServices(subject platformrbac.Subject, services []servicecatalog.Service) []servicecatalog.Service {
	out := make([]servicecatalog.Service, 0, len(services))
	for _, service := range services {
		if s.allowed(subject, service.ID, "metrics.query", "read") {
			out = append(out, service)
		}
	}
	return out
}

func (s Service) findBindings(ctx context.Context, serviceID string) ([]ServiceBinding, error) {
	var bindings []ServiceBinding
	var err error
	if serviceID == "" {
		err = s.bindings.FindAll(ctx, &bindings)
	} else {
		err = s.bindings.FindByService(ctx, serviceID, &bindings)
	}
	if err != nil {
		return nil, err
	}
	sort.SliceStable(bindings, func(i, j int) bool {
		return bindings[i].UpdatedAt.After(bindings[j].UpdatedAt)
	})
	return bindings, nil
}

func (s Service) getBinding(ctx context.Context, id string) (ServiceBinding, error) {
	if s.bindings == nil {
		return ServiceBinding{}, apperr.NotFound("指标服务绑定不存在")
	}
	var binding ServiceBinding
	if err := s.bindings.FindByID(ctx, strings.TrimSpace(id), &binding); err != nil {
		return ServiceBinding{}, normalizeNotFound(err, "指标服务绑定不存在")
	}
	return normalizeBinding(binding), nil
}

func (s Service) bindingView(ctx context.Context, binding ServiceBinding) (ServiceBindingView, error) {
	view := ServiceBindingView{Binding: normalizeBinding(binding)}
	if service, err := s.services.Get(ctx, binding.ServiceID); err == nil {
		view.Service = &service
	}
	if s.endpoints != nil {
		if endpoint, err := s.endpoints.Get(ctx, binding.EndpointID); err == nil {
			view.Endpoint = &endpoint
		}
	}
	return view, nil
}

func (s Service) metricsEndpoint(ctx context.Context, endpointID string) (obsendpoint.Endpoint, error) {
	if s.endpoints == nil {
		return obsendpoint.Endpoint{}, apperr.NotFound("指标端点不存在")
	}
	endpoint, err := s.endpoints.Get(ctx, endpointID)
	if err != nil {
		return obsendpoint.Endpoint{}, normalizeNotFound(err, "指标端点不存在")
	}
	if !obsendpoint.ContainsSignalType(endpoint, obsendpoint.SignalTypeMetrics) {
		return obsendpoint.Endpoint{}, apperr.InvalidRequest("指标服务绑定只能使用支持 metrics 的观测端点")
	}
	return endpoint, nil
}

func (s Service) ensureNoOtherActiveBinding(ctx context.Context, currentID string, serviceID string) error {
	var bindings []ServiceBinding
	if err := s.bindings.FindByService(ctx, serviceID, &bindings); err != nil {
		return err
	}
	for _, binding := range bindings {
		binding = normalizeBinding(binding)
		if binding.ID == currentID {
			continue
		}
		if binding.Status == BindingStatusActive {
			return apperr.Conflict("服务已存在 active metrics binding")
		}
	}
	return nil
}

func (s Service) allowed(subject platformrbac.Subject, serviceID string, resource string, action string) bool {
	if subject.ID == "" || subject.Type == "" || s.authorizer == nil {
		return false
	}
	decision := s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: resource,
		Action:   action,
		Scope:    platformrbac.Scope{ServiceID: serviceID},
	})
	return decision.Allowed
}

func normalizeCreateRequest(req CreateServiceBindingRequest) CreateServiceBindingRequest {
	req.ServiceID = strings.TrimSpace(req.ServiceID)
	req.EndpointID = strings.TrimSpace(req.EndpointID)
	req.Tenant.AccountID = strings.TrimSpace(req.Tenant.AccountID)
	req.Tenant.ProjectID = strings.TrimSpace(req.Tenant.ProjectID)
	req.LabelMatch = normalizeLabels(req.LabelMatch)
	req.BasePromQL = strings.TrimSpace(req.BasePromQL)
	req.Status = strings.TrimSpace(req.Status)
	return req
}

func normalizeUpdateRequest(req UpdateServiceBindingRequest) UpdateServiceBindingRequest {
	req.EndpointID = strings.TrimSpace(req.EndpointID)
	req.Tenant.AccountID = strings.TrimSpace(req.Tenant.AccountID)
	req.Tenant.ProjectID = strings.TrimSpace(req.Tenant.ProjectID)
	if req.LabelMatch != nil {
		req.LabelMatch = normalizeLabels(req.LabelMatch)
	}
	req.Status = strings.TrimSpace(req.Status)
	return req
}

func normalizeBinding(binding ServiceBinding) ServiceBinding {
	binding.ServiceID = strings.TrimSpace(binding.ServiceID)
	binding.EndpointID = strings.TrimSpace(binding.EndpointID)
	binding.Tenant.AccountID = strings.TrimSpace(binding.Tenant.AccountID)
	binding.Tenant.ProjectID = strings.TrimSpace(binding.Tenant.ProjectID)
	binding.LabelMatch = normalizeLabels(binding.LabelMatch)
	binding.BasePromQL = strings.TrimSpace(binding.BasePromQL)
	binding.Status = strings.TrimSpace(binding.Status)
	if binding.Status == "" {
		binding.Status = BindingStatusActive
	}
	binding.LastProbeStatus = strings.TrimSpace(binding.LastProbeStatus)
	if binding.LastProbeStatus == "" {
		binding.LastProbeStatus = ProbeStatusPending
	}
	binding.LastProbeMessage = strings.TrimSpace(binding.LastProbeMessage)
	return binding
}

func normalizeLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	out := map[string]string{}
	for key, value := range labels {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func validateLabelMatch(labels map[string]string) error {
	for key := range labels {
		if !allowedLabelMatchKey(key) {
			return apperr.InvalidRequest("label_match 只允许稳定低基数字段")
		}
	}
	return nil
}

func allowedLabelMatchKey(key string) bool {
	switch key {
	case "service.name", "namespace", "cluster", "job", "app.kubernetes.io/name", "k8s.namespace.name", "k8s.cluster.id":
		return true
	default:
		return false
	}
}

func validateMetricQueryScope(basePromQL string, labels map[string]string) error {
	scope := MetricScopePromQL(basePromQL, labels)
	if scope == "" || !BasePromQLMatchesLabelMatch(scope, labels) {
		return apperr.InvalidRequest("active metrics binding 必须具备可收敛服务作用域的 base_promql")
	}
	return nil
}

func MetricScopePromQL(basePromQL string, labels map[string]string) string {
	basePromQL = strings.TrimSpace(basePromQL)
	if basePromQL != "" {
		return basePromQL
	}
	return labelMatchPromQLSelector(labels)
}

func BasePromQLMatchesLabelMatch(basePromQL string, labels map[string]string) bool {
	basePromQL = strings.TrimSpace(basePromQL)
	if basePromQL == "" || len(labels) == 0 {
		return false
	}
	for _, value := range labels {
		value = strings.TrimSpace(value)
		if value == "" {
			return false
		}
		if !strings.Contains(basePromQL, strconv.Quote(value)) && !strings.Contains(basePromQL, value) {
			return false
		}
	}
	return true
}

func labelMatchPromQLSelector(labels map[string]string) string {
	normalized := normalizeLabels(labels)
	if len(normalized) == 0 {
		return ""
	}
	keys := make([]string, 0, len(normalized))
	for key := range normalized {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	matchers := make([]string, 0, len(keys))
	for _, key := range keys {
		matchers = append(matchers, metricLabelName(key)+"="+strconv.Quote(normalized[key]))
	}
	return "{" + strings.Join(matchers, ",") + "}"
}

func metricLabelName(key string) string {
	key = strings.TrimSpace(key)
	key = strings.NewReplacer(".", "_", "/", "_", "-", "_").Replace(key)
	if key == "" {
		return "label"
	}
	return key
}

func validBindingStatus(status string) bool {
	return status == BindingStatusActive || status == BindingStatusPendingVerification || status == BindingStatusDisabled
}

func copyLabels(labels map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range labels {
		out[key] = value
	}
	return out
}

func firstTenant(primary obsendpoint.EndpointTenant, fallback obsendpoint.EndpointTenant) obsendpoint.EndpointTenant {
	if primary.AccountID != "" || primary.ProjectID != "" {
		return primary
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func actorRefFromSubject(subject platformrbac.Subject) ActorRef {
	return ActorRef{
		ID:   strings.TrimSpace(subject.ID),
		Type: strings.TrimSpace(subject.Type),
		Name: strings.TrimSpace(subject.DisplayName),
	}
}

func endpointHasAnyURL(endpoint obsendpoint.Endpoint) bool {
	return strings.TrimSpace(endpoint.URLs.QueryURL) != "" ||
		strings.TrimSpace(endpoint.URLs.UIURL) != "" ||
		strings.TrimSpace(endpoint.URLs.WriteURL) != "" ||
		strings.TrimSpace(endpoint.URLs.RemoteWriteURL) != "" ||
		strings.TrimSpace(endpoint.URLs.BaseURL) != ""
}

func normalizeNotFound(err error, message string) error {
	if errors.Is(err, mongo.ErrNoDocuments) || errors.Is(err, database.ErrNotFound) {
		return apperr.NotFound(message)
	}
	var appError apperr.Error
	if errors.As(err, &appError) && appError.Status == 404 {
		return apperr.NotFound(message)
	}
	return err
}
