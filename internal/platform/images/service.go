package images

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	platformaudit "novaapm/internal/platform/audit"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/pkg/apperr"
)

type Service struct {
	repo       Repository
	authorizer Authorizer
	auditor    Auditor
}

var trustedImageVersionPattern = regexp.MustCompile(`(?:@sha256:[a-f0-9]{64}|:(?:v?\d+\.\d+\.\d+))$`)

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type Auditor interface {
	Record(ctx context.Context, event platformaudit.Event) (platformaudit.Event, error)
}

type ServiceOption func(*Service)

func WithAuthorizer(authorizer Authorizer) ServiceOption {
	return func(service *Service) { service.authorizer = authorizer }
}

func WithAuditor(auditor Auditor) ServiceOption {
	return func(service *Service) { service.auditor = auditor }
}

func NewService(repo Repository, options ...ServiceOption) Service {
	service := Service{repo: repo}
	for _, option := range options {
		option(&service)
	}
	return service
}

func (s Service) ListAuthorized(ctx context.Context, subject platformrbac.Subject) ([]Image, error) {
	if !s.allowed(subject, "read") {
		return nil, ErrPermissionDenied
	}
	return s.List(ctx)
}

func (s Service) UpsertAuthorized(ctx context.Context, subject platformrbac.Subject, req UpsertRequest) (Image, error) {
	if !s.allowed(subject, "manage") {
		return Image{}, ErrPermissionDenied
	}
	item, err := s.Upsert(ctx, req)
	if err != nil {
		return Image{}, err
	}
	if s.auditor != nil {
		_, err = s.auditor.Record(ctx, platformaudit.Event{
			Actor:    platformaudit.Actor{ID: subject.ID, Name: subject.DisplayName},
			Resource: platformaudit.Resource{Type: "platform_image", Name: item.Key},
			Action:   "update", Scope: "global", RequestSummary: map[string]any{"value": item.Value}, Result: "success",
		})
		if err != nil {
			return Image{}, err
		}
	}
	return item, nil
}

func (s Service) allowed(subject platformrbac.Subject, action string) bool {
	if subject.ID == "" || subject.Type == "" || s.authorizer == nil {
		return false
	}
	return s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "platform.image", Action: action, Scope: platformrbac.Scope{Global: true},
	}).Allowed
}

func (s Service) List(ctx context.Context) ([]Image, error) {
	if s.repo == nil {
		return nil, ErrUnavailable
	}
	values, err := s.TemplateValues(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Image, 0, len(values))
	for key, value := range values {
		items = append(items, Image{Key: key, Value: value})
	}
	sort.SliceStable(items, func(left, right int) bool {
		return items[left].Key < items[right].Key
	})
	return items, nil
}

func (s Service) Upsert(ctx context.Context, req UpsertRequest) (Image, error) {
	if s.repo == nil {
		return Image{}, ErrUnavailable
	}
	item := Image{
		Key:       strings.TrimSpace(req.Key),
		Value:     strings.TrimSpace(req.Value),
		UpdatedAt: time.Now().UTC(),
	}
	if err := validateImage(item); err != nil {
		return Image{}, err
	}
	if err := s.repo.Upsert(ctx, item); err != nil {
		return Image{}, err
	}
	return item, nil
}

func (s Service) TemplateValues(ctx context.Context) (map[string]string, error) {
	if s.repo == nil {
		return nil, ErrUnavailable
	}
	values := defaultTemplateValues()
	items, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		key := strings.TrimSpace(item.Key)
		value := strings.TrimSpace(item.Value)
		if _, ok := DefaultTemplateValues[key]; ok && value != "" {
			if err := validateImage(Image{Key: key, Value: value}); err != nil {
				return nil, err
			}
			values[key] = value
		}
	}
	return values, nil
}

func ApplyTemplateValues(input string, values map[string]string) string {
	output := input
	for key, value := range values {
		output = strings.ReplaceAll(output, key, value)
	}
	return output
}

func defaultTemplateValues() map[string]string {
	values := make(map[string]string, len(DefaultTemplateValues))
	for key, value := range DefaultTemplateValues {
		values[key] = value
	}
	return values
}

func validateImage(item Image) error {
	if item.Key == "" {
		return apperr.InvalidRequest("镜像占位符 key 不能为空")
	}
	if _, ok := DefaultTemplateValues[item.Key]; !ok {
		return apperr.InvalidRequest("未知镜像占位符")
	}
	if item.Value == "" {
		return apperr.InvalidRequest("镜像地址不能为空")
	}
	if strings.ContainsAny(item.Value, " \t\r\n") {
		return apperr.InvalidRequest("镜像地址不能包含空白字符")
	}
	if !strings.HasPrefix(item.Value, "hub-test.service.ucloud.cn/logsplatfrom/") {
		return apperr.InvalidRequest("运行时镜像必须来自平台受信任仓库 hub-test.service.ucloud.cn/logsplatfrom")
	}
	version := item.Value[strings.LastIndex(item.Value, "/")+1:]
	if !trustedImageVersionPattern.MatchString(version) {
		return apperr.InvalidRequest("运行时镜像必须使用不可含 latest 的语义化版本或 sha256 digest")
	}
	return nil
}
