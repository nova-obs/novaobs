package alerting

import (
	"context"
	"testing"

	"novaobs/internal/database/memstore"

	"github.com/stretchr/testify/require"
)

func TestServiceCreatesAndListsRules(t *testing.T) {
	store := memstore.NewStore()
	service := NewService(store.AlertRules())
	ctx := context.Background()

	rule, err := service.Create(ctx, Rule{
		Name:     "high-error-rate",
		RuleType: "logs",
		Source:   "victorialogs",
		Severity: "critical",
	})
	require.NoError(t, err)
	require.NotEmpty(t, rule.ID)
	require.Equal(t, "draft", rule.Status)

	rules, err := service.List(ctx)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.Equal(t, "high-error-rate", rules[0].Name)
}

func TestServiceCreatesMultipleRules(t *testing.T) {
	store := memstore.NewStore()
	service := NewService(store.AlertRules())
	ctx := context.Background()

	_, err := service.Create(ctx, Rule{Name: "rule-a", RuleType: "logs", Severity: "warning"})
	require.NoError(t, err)
	_, err = service.Create(ctx, Rule{Name: "rule-b", RuleType: "metrics", Severity: "critical"})
	require.NoError(t, err)
	_, err = service.Create(ctx, Rule{Name: "rule-c", RuleType: "chain", Severity: "info"})
	require.NoError(t, err)

	rules, err := service.List(ctx)
	require.NoError(t, err)
	require.Len(t, rules, 3)
}
