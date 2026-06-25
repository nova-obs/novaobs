package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"novaobs/internal/alerting"
	"novaobs/internal/database/memstore"
	"novaobs/internal/platform/audit"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAlertmanagerWebhookRequiresTokenAndPersistsEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := memstore.NewStore()
	repository := alerting.NewStoreRepository(store.Alerting())
	require.NoError(t, repository.SaveChange(context.Background(), alerting.ChangeSet{
		Rule:       alerting.Rule{ID: "rule-a", Spec: alerting.RuleSpec{Scope: alerting.RuleScope{ServiceID: "service-a"}}, State: alerting.RuleStateEnabled},
		Update:     alerting.UpdateRecord{ID: "update-a", RuleID: "rule-a"},
		Deployment: alerting.Deployment{ID: "deployment-a", RuleID: "rule-a"},
		Audit:      audit.Event{ID: "audit-a"},
	}))
	handler := alertmanagerWebhookHandler(alerting.NewEventIngestor(repository, repository, "webhook-secret", nil))
	router := gin.New()
	router.POST("/webhook", handler)
	body := `{"status":"firing","alerts":[{"status":"firing","fingerprint":"abc","labels":{"novaobs_rule_id":"rule-a","service_id":"service-a"},"annotations":{"summary":"支付失败"},"startsAt":"2026-06-22T09:59:00Z"}]}`

	unauthorized := httptest.NewRecorder()
	unauthorizedRequest := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	unauthorizedRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(unauthorized, unauthorizedRequest)
	require.Equal(t, http.StatusUnauthorized, unauthorized.Code)

	request := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer webhook-secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	var events []alerting.AlertEvent
	require.NoError(t, store.Alerting().FindAlertEvents(request.Context(), "rule-a", "abc", 20, &events))
	require.Len(t, events, 1)
}
