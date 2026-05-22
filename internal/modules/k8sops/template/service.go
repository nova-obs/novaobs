package template

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"novaobs/internal/platform/audit"
	platformrbac "novaobs/internal/platform/rbac"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var (
	ErrPermissionDenied = errors.New("permission_denied")
	ErrInvalidRequest   = errors.New("invalid_k8s_template_request")
	ErrNotFound         = errors.New("k8s_template_not_found")
	ErrAlreadyExists    = errors.New("k8s_template_already_exists")
)

type Repository interface {
	List(ctx context.Context, filter ListFilter) ([]Template, error)
	Create(ctx context.Context, item Template) (Template, error)
	Update(ctx context.Context, item Template) (Template, error)
	Delete(ctx context.Context, id string) (Template, error)
	Get(ctx context.Context, id string) (Template, error)
}

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type Auditor interface {
	Record(ctx context.Context, event audit.Event) (audit.Event, error)
}

type Service struct {
	repo       Repository
	authorizer Authorizer
	auditor    Auditor
}

func NewService(repo Repository, authorizer Authorizer, auditor Auditor) Service {
	if authorizer == nil {
		authorizer = denyAuthorizer{}
	}
	if auditor == nil {
		auditor = noopAuditor{}
	}
	return Service{repo: repo, authorizer: authorizer, auditor: auditor}
}

func (s Service) List(ctx context.Context, filter ListFilter) ([]Template, error) {
	return s.repo.List(ctx, filter)
}

func (s Service) Create(ctx context.Context, subject platformrbac.Subject, req UpsertRequest) (Template, audit.Event, error) {
	req = normalizeUpsertRequest(req)
	if req.Name == "" || req.Type == "" || req.YAMLContent == "" {
		return Template{}, audit.Event{}, ErrInvalidRequest
	}
	if !s.allowed(subject, "create") {
		return Template{}, audit.Event{}, ErrPermissionDenied
	}
	now := time.Now().UTC()
	item := Template{
		ID:          primitive.NewObjectID().Hex(),
		Name:        req.Name,
		Type:        req.Type,
		YAMLContent: req.YAMLContent,
		Variables:   cloneVariables(req.Variables),
		Description: req.Description,
		Source:      "novaobs",
		CreatedBy:   subject.ID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	created, err := s.repo.Create(ctx, item)
	if err != nil {
		return Template{}, audit.Event{}, err
	}
	event, err := s.record(ctx, subject, "create", created.Name, map[string]any{
		"template_id": created.ID,
		"name":        created.Name,
		"type":        created.Type,
		"variables":   variableNames(created.Variables),
		"yaml_bytes":  len(created.YAMLContent),
	})
	if err != nil {
		_, _ = s.repo.Delete(ctx, created.ID)
		return Template{}, audit.Event{}, err
	}
	return created, event, nil
}

func (s Service) Update(ctx context.Context, subject platformrbac.Subject, req UpsertRequest) (Template, audit.Event, error) {
	req = normalizeUpsertRequest(req)
	if req.ID == "" || req.Name == "" || req.Type == "" || req.YAMLContent == "" {
		return Template{}, audit.Event{}, ErrInvalidRequest
	}
	if !s.allowed(subject, "update") {
		return Template{}, audit.Event{}, ErrPermissionDenied
	}
	previous, err := s.repo.Get(ctx, req.ID)
	if err != nil {
		return Template{}, audit.Event{}, err
	}
	item := previous
	item.Name = req.Name
	item.Type = req.Type
	item.YAMLContent = req.YAMLContent
	item.Variables = cloneVariables(req.Variables)
	item.Description = req.Description
	item.UpdatedAt = time.Now().UTC()
	updated, err := s.repo.Update(ctx, item)
	if err != nil {
		return Template{}, audit.Event{}, err
	}
	event, err := s.record(ctx, subject, "update", updated.Name, map[string]any{
		"template_id": updated.ID,
		"name":        updated.Name,
		"type":        updated.Type,
		"variables":   variableNames(updated.Variables),
		"yaml_bytes":  len(updated.YAMLContent),
	})
	if err != nil {
		_, _ = s.repo.Update(ctx, previous)
		return Template{}, audit.Event{}, err
	}
	return updated, event, nil
}

func (s Service) Delete(ctx context.Context, subject platformrbac.Subject, req DeleteRequest) (audit.Event, error) {
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		return audit.Event{}, ErrInvalidRequest
	}
	if !s.allowed(subject, "delete") {
		return audit.Event{}, ErrPermissionDenied
	}
	deleted, err := s.repo.Delete(ctx, req.ID)
	if err != nil {
		return audit.Event{}, err
	}
	event, err := s.record(ctx, subject, "delete", deleted.Name, map[string]any{
		"template_id": deleted.ID,
		"name":        deleted.Name,
		"type":        deleted.Type,
	})
	if err != nil {
		_, _ = s.repo.Create(ctx, deleted)
		return audit.Event{}, err
	}
	return event, nil
}

func (s Service) Render(ctx context.Context, subject platformrbac.Subject, req RenderRequest) (RenderResult, error) {
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		return RenderResult{}, ErrInvalidRequest
	}
	if !s.allowed(subject, "render") {
		return RenderResult{}, ErrPermissionDenied
	}
	item, err := s.repo.Get(ctx, req.ID)
	if err != nil {
		return RenderResult{}, err
	}
	rendered, err := renderYAML(item, req.Variables)
	if err != nil {
		return RenderResult{}, err
	}
	event, err := s.record(ctx, subject, "render", item.Name, map[string]any{
		"template_id":   item.ID,
		"name":          item.Name,
		"type":          item.Type,
		"variable_keys": variableKeys(req.Variables),
	})
	if err != nil {
		return RenderResult{}, err
	}
	return RenderResult{RenderedYAML: rendered, AuditID: event.ID}, nil
}

func BaseTemplate(templateType string) (BaseTemplateResult, error) {
	kind := normalizeTemplateType(templateType)
	content, variables, ok := baseTemplateContent(kind)
	if !ok {
		return BaseTemplateResult{}, ErrInvalidRequest
	}
	return BaseTemplateResult{
		Type:        kind,
		YAMLContent: content,
		Variables:   variables,
		Description: "NovaObs 内置 K8s 发布模板基线",
		Source:      "novaobs-base",
	}, nil
}

func (s Service) allowed(subject platformrbac.Subject, action string) bool {
	decision := s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "k8s.template",
		Action:   action,
		Scope:    platformrbac.Scope{Global: true},
	})
	return decision.Allowed
}

func (s Service) record(ctx context.Context, subject platformrbac.Subject, action string, name string, summary map[string]any) (audit.Event, error) {
	return s.auditor.Record(ctx, audit.Event{
		Actor:          audit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource:       audit.Resource{Type: "k8s.template", Name: name},
		ResourceType:   "k8s.template",
		ResourceName:   name,
		Action:         action,
		Scope:          "global",
		Result:         "success",
		RequestSummary: summary,
	})
}

type MemoryRepository struct {
	mu    sync.Mutex
	items []Template
}

func NewMemoryRepository(items []Template) *MemoryRepository {
	copied := append([]Template(nil), items...)
	return &MemoryRepository{items: copied}
}

func (r *MemoryRepository) List(_ context.Context, filter ListFilter) ([]Template, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	templateType := strings.ToLower(strings.TrimSpace(filter.Type))
	out := make([]Template, 0, len(r.items))
	for _, item := range r.items {
		if templateType != "" && strings.ToLower(item.Type) != templateType {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.Name), query) && !strings.Contains(strings.ToLower(item.Description), query) {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(left, right int) bool { return out[left].Name < out[right].Name })
	return paginate(out, filter.Page, filter.PageSize), nil
}

func (r *MemoryRepository) Create(_ context.Context, item Template) (Template, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.items {
		if existing.ID == item.ID || strings.EqualFold(existing.Name, item.Name) {
			return Template{}, ErrAlreadyExists
		}
	}
	r.items = append(r.items, item)
	return item, nil
}

func (r *MemoryRepository) Update(_ context.Context, item Template) (Template, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for idx, existing := range r.items {
		if existing.ID == item.ID {
			r.items[idx] = item
			return item, nil
		}
	}
	return Template{}, ErrNotFound
}

func (r *MemoryRepository) Delete(_ context.Context, id string) (Template, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.items[:0]
	deleted := Template{}
	for _, item := range r.items {
		if item.ID == id {
			deleted = item
			continue
		}
		next = append(next, item)
	}
	if deleted.ID == "" {
		return Template{}, ErrNotFound
	}
	r.items = next
	return deleted, nil
}

func (r *MemoryRepository) Get(_ context.Context, id string) (Template, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range r.items {
		if item.ID == id {
			return item, nil
		}
	}
	return Template{}, ErrNotFound
}

func normalizeUpsertRequest(req UpsertRequest) UpsertRequest {
	req.ID = strings.TrimSpace(req.ID)
	req.Name = strings.TrimSpace(req.Name)
	req.Type = normalizeTemplateType(req.Type)
	req.YAMLContent = strings.TrimSpace(req.YAMLContent)
	req.Description = strings.TrimSpace(req.Description)
	req.Variables = cloneVariables(req.Variables)
	return req
}

func normalizeTemplateType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "deployment", "deployments":
		return "Deployment"
	case "service", "services":
		return "Service"
	case "statefulset", "statefulsets":
		return "StatefulSet"
	case "configmap", "configmaps":
		return "ConfigMap"
	case "ingress", "ingresses":
		return "Ingress"
	case "horizontalpodautoscaler", "horizontalpodautoscalers", "hpa":
		return "HorizontalPodAutoscaler"
	case "gateway", "gateways":
		return "Gateway"
	case "virtualservice", "virtualservices":
		return "VirtualService"
	case "destinationrule", "destinationrules":
		return "DestinationRule"
	case "envoyfilter", "envoyfilters":
		return "EnvoyFilter"
	default:
		return strings.TrimSpace(value)
	}
}

func baseTemplateContent(kind string) (string, []Variable, bool) {
	switch kind {
	case "Deployment":
		return `apiVersion: apps/v1
kind: Deployment
metadata:
  name: <<name>>
  namespace: <<namespace>>
spec:
  replicas: <<replicas>>
  selector:
    matchLabels:
      app: <<app>>
  template:
    metadata:
      labels:
        app: <<app>>
    spec:
      containers:
        - name: <<container>>
          image: <<image>>
          ports:
            - containerPort: <<port>>`, baseVariables("name", "namespace", "replicas", "app", "container", "image", "port"), true
	case "StatefulSet":
		return `apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: <<name>>
  namespace: <<namespace>>
spec:
  serviceName: <<service>>
  replicas: <<replicas>>
  selector:
    matchLabels:
      app: <<app>>
  template:
    metadata:
      labels:
        app: <<app>>
    spec:
      containers:
        - name: <<container>>
          image: <<image>>`, baseVariables("name", "namespace", "service", "replicas", "app", "container", "image"), true
	case "Service":
		return `apiVersion: v1
kind: Service
metadata:
  name: <<name>>
  namespace: <<namespace>>
spec:
  selector:
    app: <<app>>
  ports:
    - name: http
      port: <<port>>
      targetPort: <<target_port>>`, baseVariables("name", "namespace", "app", "port", "target_port"), true
	case "ConfigMap":
		return `apiVersion: v1
kind: ConfigMap
metadata:
  name: <<name>>
  namespace: <<namespace>>
data:
  config.yaml: |
    key: <<value>>`, baseVariables("name", "namespace", "value"), true
	case "Ingress":
		return `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: <<name>>
  namespace: <<namespace>>
spec:
  rules:
    - host: <<host>>
      http:
        paths:
          - path: <<path>>
            pathType: Prefix
            backend:
              service:
                name: <<service>>
                port:
                  number: <<port>>`, baseVariables("name", "namespace", "host", "path", "service", "port"), true
	case "HorizontalPodAutoscaler":
		return `apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: <<name>>
  namespace: <<namespace>>
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: <<target>>
  minReplicas: <<min_replicas>>
  maxReplicas: <<max_replicas>>`, baseVariables("name", "namespace", "target", "min_replicas", "max_replicas"), true
	case "Gateway":
		return `apiVersion: networking.istio.io/v1beta1
kind: Gateway
metadata:
  name: <<name>>
  namespace: <<namespace>>
spec:
  selector:
    istio: ingressgateway
  servers:
    - port:
        number: <<port>>
        name: http
        protocol: HTTP
      hosts:
        - <<host>>`, baseVariables("name", "namespace", "port", "host"), true
	case "VirtualService":
		return `apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: <<name>>
  namespace: <<namespace>>
spec:
  hosts:
    - <<host>>
  gateways:
    - <<gateway>>
  http:
    - route:
        - destination:
            host: <<service>>
            port:
              number: <<port>>`, baseVariables("name", "namespace", "host", "gateway", "service", "port"), true
	case "DestinationRule":
		return `apiVersion: networking.istio.io/v1beta1
kind: DestinationRule
metadata:
  name: <<name>>
  namespace: <<namespace>>
spec:
  host: <<host>>
  trafficPolicy:
    loadBalancer:
      simple: ROUND_ROBIN`, baseVariables("name", "namespace", "host"), true
	case "EnvoyFilter":
		return `apiVersion: networking.istio.io/v1alpha3
kind: EnvoyFilter
metadata:
  name: <<name>>
  namespace: <<namespace>>
spec:
  workloadSelector:
    labels:
      app: <<app>>
  configPatches: []`, baseVariables("name", "namespace", "app"), true
	default:
		return "", nil, false
	}
}

func baseVariables(names ...string) []Variable {
	variables := make([]Variable, 0, len(names))
	for _, name := range names {
		variables = append(variables, Variable{Name: name, Required: true})
	}
	return variables
}

func renderYAML(item Template, values map[string]string) (string, error) {
	rendered := item.YAMLContent
	for key, value := range values {
		rendered = strings.ReplaceAll(rendered, "<<"+key+">>", value)
	}
	for _, variable := range item.Variables {
		if variable.Required && strings.Contains(rendered, "<<"+variable.Name+">>") {
			return "", fmt.Errorf("%w: required variable %q was not provided", ErrInvalidRequest, variable.Name)
		}
	}
	return rendered, nil
}

func cloneVariables(items []Variable) []Variable {
	out := append([]Variable(nil), items...)
	for idx := range out {
		out[idx].Name = strings.TrimSpace(out[idx].Name)
		out[idx].Description = strings.TrimSpace(out[idx].Description)
		out[idx].DefaultValue = strings.TrimSpace(out[idx].DefaultValue)
	}
	return out
}

func variableNames(items []Variable) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.Name != "" {
			out = append(out, item.Name)
		}
	}
	sort.Strings(out)
	return out
}

func variableKeys(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func paginate[T any](items []T, page int, pageSize int) []T {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		return items
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []T{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

type noopAuditor struct{}

func (noopAuditor) Record(ctx context.Context, event audit.Event) (audit.Event, error) {
	return event, nil
}
