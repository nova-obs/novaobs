package images

import (
	"context"
	"sort"
	"strings"
	"time"

	"novaobs/pkg/apperr"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return Service{repo: repo}
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
	return nil
}
