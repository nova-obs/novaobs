package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOKWritesEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	OK(ctx, gin.H{"name": "platform"}, gin.H{"total": 1})

	require.Equal(t, http.StatusOK, recorder.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, true, body["success"])
	require.NotNil(t, body["data"])
	require.NotNil(t, body["meta"])
	require.Nil(t, body["error"])
}

func TestErrorWritesEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	Error(ctx, http.StatusBadRequest, "invalid_request", "请求参数无效")

	require.Equal(t, http.StatusBadRequest, recorder.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, false, body["success"])
	require.Nil(t, body["data"])
	require.NotNil(t, body["error"])
}
