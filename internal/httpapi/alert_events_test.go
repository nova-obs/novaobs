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

func TestAlertIngestRequiresTokenAndPersistsEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := memstore.NewStore()
	repository := alerting.NewStoreRepository(store.Alerting())
	require.NoError(t, repository.SaveChange(context.Background(), alerting.ChangeSet{
		Rule:   alerting.Rule{ID: "rule-a", Spec: alerting.RuleSpec{Scope: alerting.RuleScope{ServiceID: "service-a"}}, State: alerting.RuleStateEnabled},
		Update: alerting.UpdateRecord{ID: "update-a", RuleID: "rule-a"},
		Audit:  audit.Event{ID: "audit-a"},
	}))
	handler := alertIngestHandler(alerting.NewEventIngestor(repository, repository, "ingest-secret", nil))
	router := gin.New()
	router.POST("/ingest", handler)
	body := `[{"status":"firing","fingerprint":"abc","labels":{"novaobs_rule_id":"rule-a","service_id":"service-a"},"annotations":{"summary":"支付失败"},"startsAt":"2026-06-22T09:59:00Z"}]`

	unauthorized := httptest.NewRecorder()
	unauthorizedRequest := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(body))
	unauthorizedRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(unauthorized, unauthorizedRequest)
	require.Equal(t, http.StatusUnauthorized, unauthorized.Code)

	request := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer ingest-secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	var events []alerting.AlertEvent
	require.NoError(t, store.Alerting().FindAlertEvents(request.Context(), "rule-a", "abc", 20, &events))
	require.Len(t, events, 1)
}

func TestAlertIngestAcceptsDirectVmalertAlerts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := memstore.NewStore()
	repository := alerting.NewStoreRepository(store.Alerting())
	require.NoError(t, repository.SaveChange(context.Background(), alerting.ChangeSet{
		Rule:   alerting.Rule{ID: "rule-a", Spec: alerting.RuleSpec{Scope: alerting.RuleScope{ServiceID: "service-a"}}, State: alerting.RuleStateEnabled},
		Update: alerting.UpdateRecord{ID: "update-a", RuleID: "rule-a"},
		Audit:  audit.Event{ID: "audit-a"},
	}))
	router := gin.New()
	router.POST("/ingest", alertIngestHandler(alerting.NewEventIngestor(repository, repository, "ingest-secret", nil)))
	body := `[{"fingerprint":"abc","labels":{"novaobs_rule_id":"rule-a","novaobs_runtime_id":"vmalert-logs:vl-prod","service_id":"service-a"},"annotations":{"summary":"支付失败"},"startsAt":"2026-06-22T09:59:00Z"}]`

	request := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer ingest-secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	var events []alerting.AlertEvent
	require.NoError(t, store.Alerting().FindAlertEvents(request.Context(), "rule-a", "abc", 20, &events))
	require.Len(t, events, 1)
	require.Equal(t, "vmalert-logs:vl-prod", events[0].SourceRuntimeID)
}

func TestVmalertNotifierRouteAcceptsDirectAlerts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := memstore.NewStore()
	repository := alerting.NewStoreRepository(store.Alerting())
	require.NoError(t, repository.SaveChange(context.Background(), alerting.ChangeSet{
		Rule:   alerting.Rule{ID: "rule-a", Spec: alerting.RuleSpec{Scope: alerting.RuleScope{ServiceID: "service-a"}}, State: alerting.RuleStateEnabled},
		Update: alerting.UpdateRecord{ID: "update-a", RuleID: "rule-a"},
		Audit:  audit.Event{ID: "audit-a"},
	}))
	router := NewRouter(Dependencies{
		Store:              store,
		AlertEventIngestor: alerting.NewEventIngestor(repository, repository, "ingest-secret", nil),
	})
	body := `[{"fingerprint":"abc","labels":{"novaobs_rule_id":"rule-a","service_id":"service-a"},"startsAt":"2026-06-22T09:59:00Z"}]`

	request := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer ingest-secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"accepted":1`)
}

func TestLegacyAlertmanagerWebhookRouteIsRemoved(t *testing.T) {
	env := newTestRouter(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/webhook/alertmanager", strings.NewReader(`{"alerts":[]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	env.router.ServeHTTP(response, request)

	require.Equal(t, http.StatusNotFound, response.Code)
}
