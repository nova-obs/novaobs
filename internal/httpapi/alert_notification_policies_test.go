package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNotificationPolicyLifecycleAPI(t *testing.T) {
	env := newTestRouter(t)
	created := performJSON(t, env.router, http.MethodPost, "/api/v1/alerts/notification-policies", `{"name":"支付值班","alertmanager_receiver":"pay-oncall","enabled":true}`)
	policyID := nestedString(t, created, "data", "id")

	listed := performJSON(t, env.router, http.MethodGet, "/api/v1/alerts/notification-policies?enabled=true", "")
	require.Len(t, nestedValue(t, listed, "data").([]any), 1)

	updated := performJSON(t, env.router, http.MethodPut, "/api/v1/alerts/notification-policies/"+policyID, `{"name":"支付值班（主）","alertmanager_receiver":"pay-oncall","enabled":true}`)
	require.Equal(t, "支付值班（主）", nestedString(t, updated, "data", "name"))
}

func TestNotificationPolicyRejectsURLAsReceiver(t *testing.T) {
	env := newTestRouter(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/notification-policies", bytes.NewBufferString(`{
        "name":"bad","alertmanager_receiver":"https://example.com/hook","enabled":true
    }`))
	request.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
}
