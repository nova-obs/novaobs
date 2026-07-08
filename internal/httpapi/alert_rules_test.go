package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"novaobs/internal/alerting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAlertRuleLifecycleAPI(t *testing.T) {
	env := newTestRouter(t)
	created := performJSON(t, env.router, http.MethodPost, "/api/v1/alerts/rules", validAlertRuleBody(3))
	ruleID := nestedString(t, created, "data", "rule", "id")
	firstUpdateID := nestedString(t, created, "data", "update", "id")

	detail := performJSON(t, env.router, http.MethodGet, "/api/v1/alerts/rules/"+ruleID, "")
	require.Equal(t, ruleID, nestedString(t, detail, "data", "id"))

	updated := performJSON(t, env.router, http.MethodPut, "/api/v1/alerts/rules/"+ruleID, validAlertRuleBody(5))
	require.Equal(t, float64(5), nestedValue(t, updated, "data", "rule", "spec", "trigger", "threshold"))

	history := performJSON(t, env.router, http.MethodGet, "/api/v1/alerts/rules/"+ruleID+"/updates", "")
	require.Len(t, nestedValue(t, history, "data").([]any), 2)

	rolledBack := performJSON(t, env.router, http.MethodPost, "/api/v1/alerts/rules/"+ruleID+"/rollback", `{"update_id":"`+firstUpdateID+`"}`)
	require.Equal(t, float64(3), nestedValue(t, rolledBack, "data", "rule", "spec", "trigger", "threshold"))

	disabled := performJSON(t, env.router, http.MethodPost, "/api/v1/alerts/rules/"+ruleID+"/disable", `{}`)
	require.Equal(t, "disabled", nestedString(t, disabled, "data", "rule", "state"))
}

func TestAlertRuleAPIRejectsInvalidSpec(t *testing.T) {
	env := newTestRouter(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/rules", bytes.NewBufferString(`{"spec":{"name":"bad"}}`))
	request.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"invalid_alert_rule"`)
}

func TestAlertRuleAPIRequiresAuthenticatedSubject(t *testing.T) {
	router := gin.New()
	router.GET("/api/v1/alerts/rules", listAlertRulesHandler(alerting.Service{}))
	router.POST("/api/v1/alerts/rules", createAlertRuleHandler(alerting.Service{}))
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/alerts/rules", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/alerts/rules", bytes.NewBufferString(`{"spec":{}}`)),
	} {
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	}
}

func TestAlertRuleAPIRejectsDuplicateNameWithinService(t *testing.T) {
	env := newTestRouter(t)
	_ = performJSON(t, env.router, http.MethodPost, "/api/v1/alerts/rules", validAlertRuleBody(3))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/rules", bytes.NewBufferString(validAlertRuleBody(3)))
	request.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusConflict, recorder.Code)
}

func performJSON(t *testing.T, handler http.Handler, method string, path string, body string) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	handler.ServeHTTP(recorder, request)
	require.Contains(t, []int{http.StatusOK, http.StatusCreated}, recorder.Code, recorder.Body.String())
	var result map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	return result
}

func validAlertRuleBody(threshold int) string {
	return `{"spec":{"name":"orders-error-count","scope":{"service_id":"service-inventory","service_name":"inventory-api","log_route_id":"route-inventory","endpoint_id":"vl-prod","account_id":"1","project_id":"1"},"query":{"mode":"contains","expression":"level=error"},"trigger":{"mode":"window","aggregation":"count","operator":"gte","threshold":` + string(rune('0'+threshold)) + `,"window":"1m","evaluation_interval":"30s"},"grouping":{"max_instances":100},"notification":{"policy_id":"inventory-oncall","severity":"critical","owner_team":"inventory-team"}}}`
}

func nestedString(t *testing.T, root map[string]any, path ...string) string {
	t.Helper()
	return nestedValue(t, root, path...).(string)
}

func nestedValue(t *testing.T, root map[string]any, path ...string) any {
	t.Helper()
	var current any = root
	for _, key := range path {
		switch value := current.(type) {
		case map[string]any:
			next, ok := value[key]
			require.True(t, ok, "缺少路径 %v", path)
			current = next
		case []any:
			index, err := strconv.Atoi(key)
			require.NoError(t, err, "路径 %v 需要数组下标", path)
			require.GreaterOrEqual(t, index, 0, "路径 %v 数组下标不能为负数", path)
			require.Less(t, index, len(value), "路径 %v 数组下标越界", path)
			current = value[index]
		default:
			require.Failf(t, "路径类型错误", "路径 %v 不能继续读取 %q", path, key)
		}
	}
	return current
}
