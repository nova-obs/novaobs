package alerting

import (
	"context"

	"novaobs/internal/database"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Service struct {
	store database.AlertRuleStore
}

func NewService(store database.AlertRuleStore) Service {
	return Service{store: store}
}

func (s Service) List(ctx context.Context) ([]Rule, error) {
	var rules []Rule
	err := s.store.FindAll(ctx, &rules)
	return rules, err
}

func (s Service) Create(ctx context.Context, rule Rule) (Rule, error) {
	if rule.Status == "" {
		rule.Status = "draft"
	}
	if rule.ID == "" {
		rule.ID = primitive.NewObjectID().Hex()
	}
	if err := s.store.Insert(ctx, rule); err != nil {
		return Rule{}, err
	}
	return rule, nil
}
