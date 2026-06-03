package platformaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"novaobs/internal/platform/audit"
	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCreateBindingRequiresPlatformAccessManagePermission(t *testing.T) {
	router, _ := newPlatformAccessRouter(t, platformAccessReaderRepo(), platformrbac.Subject{ID: "reader-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "operator-1",
		SubjectType:   "user",
		ClusterID:     "prod",
		Namespace:     "orders",
		PermissionIDs: []string{"k8s.resource:read"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "permission_denied")
}

func TestCreateBindingGrantsNamespaceScopedResourceRead(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, auditStore := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "operator-1",
		SubjectType:   "user",
		ClusterID:     "prod",
		Namespace:     "orders",
		PermissionIDs: []string{"k8s.resource:read"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	decision := platformrbac.NewService(repo).Authorize(platformrbac.Subject{ID: "operator-1", Type: "user"}, platformrbac.Request{
		Resource: "k8s.resource",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
	})
	require.True(t, decision.Allowed)
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "k8s.platform-access", events[0].ResourceType)
}

func TestCreateBindingCanGrantTerminalExecSeparately(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, _ := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "operator-2",
		SubjectType:   "user",
		ClusterID:     "prod",
		Namespace:     "orders",
		PermissionIDs: []string{"k8s.terminal:exec"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	rbacSvc := platformrbac.NewService(repo)
	terminal := rbacSvc.Authorize(platformrbac.Subject{ID: "operator-2", Type: "user"}, platformrbac.Request{
		Resource: "k8s.terminal",
		Action:   "exec",
		Scope:    platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
	})
	read := rbacSvc.Authorize(platformrbac.Subject{ID: "operator-2", Type: "user"}, platformrbac.Request{
		Resource: "k8s.resource",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
	})
	require.True(t, terminal.Allowed)
	require.False(t, read.Allowed)
}

func TestCreateBindingCanGrantKubeconfigExportSeparately(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, _ := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "operator-3",
		SubjectType:   "user",
		ClusterID:     "test03",
		Namespace:     "logplatform",
		PermissionIDs: []string{"k8s.kubeconfig:export"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	rbacSvc := platformrbac.NewService(repo)
	export := rbacSvc.Authorize(platformrbac.Subject{ID: "operator-3", Type: "user"}, platformrbac.Request{
		Resource: "k8s.kubeconfig",
		Action:   "export",
		Scope:    platformrbac.Scope{ClusterID: "test03", Namespace: "logplatform"},
	})
	terminal := rbacSvc.Authorize(platformrbac.Subject{ID: "operator-3", Type: "user"}, platformrbac.Request{
		Resource: "k8s.terminal",
		Action:   "exec",
		Scope:    platformrbac.Scope{ClusterID: "test03", Namespace: "logplatform"},
	})
	require.True(t, export.Allowed)
	require.False(t, terminal.Allowed)
	require.Contains(t, recorder.Body.String(), "k8s.kubeconfig:export")
}

func TestCreateBindingDeployApplyGrantsPreviewAndDeploy(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, _ := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "operator-4",
		SubjectType:   "user",
		ClusterID:     "prod",
		Namespace:     "orders",
		PermissionIDs: []string{"k8s.deploy:apply"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	rbacSvc := platformrbac.NewService(repo)
	for _, action := range []string{"preview", "deploy"} {
		decision := rbacSvc.Authorize(platformrbac.Subject{ID: "operator-4", Type: "user"}, platformrbac.Request{
			Resource: "k8s.deployment",
			Action:   action,
			Scope:    platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
		})
		require.True(t, decision.Allowed, action)
	}
	deleteDecision := rbacSvc.Authorize(platformrbac.Subject{ID: "operator-4", Type: "user"}, platformrbac.Request{
		Resource: "k8s.deployment",
		Action:   "delete",
		Scope:    platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
	})
	require.False(t, deleteDecision.Allowed)
}

func TestCreateBindingRbacManageDoesNotGrantClusterRBAC(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, _ := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "operator-4",
		SubjectType:   "user",
		ClusterID:     "prod",
		Namespace:     "orders",
		PermissionIDs: []string{"k8s.rbac:manage"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	rbacSvc := platformrbac.NewService(repo)
	namespaceDecision := rbacSvc.Authorize(platformrbac.Subject{ID: "operator-4", Type: "user"}, platformrbac.Request{
		Resource: "k8s.rbac",
		Action:   "create",
		Scope:    platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
	})
	clusterDecision := rbacSvc.Authorize(platformrbac.Subject{ID: "operator-4", Type: "user"}, platformrbac.Request{
		Resource: "k8s.rbac",
		Action:   "create",
		Scope:    platformrbac.Scope{ClusterID: "prod"},
	})
	require.True(t, namespaceDecision.Allowed)
	require.False(t, clusterDecision.Allowed)
}

func TestCreateBindingClusterRBACRequiresExplicitPermission(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, _ := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "operator-4",
		SubjectType:   "user",
		ClusterID:     "prod",
		RiskAccepted:  true,
		PermissionIDs: []string{"k8s.cluster-rbac:manage"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	decision := platformrbac.NewService(repo).Authorize(platformrbac.Subject{ID: "operator-4", Type: "user"}, platformrbac.Request{
		Resource: "k8s.rbac",
		Action:   "create",
		Scope:    platformrbac.Scope{ClusterID: "prod"},
	})
	require.True(t, decision.Allowed)
}

func TestCreateBindingTemplateManageRequiresGlobalScope(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, _ := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user"})

	scopedRecorder := httptest.NewRecorder()
	scopedRequest := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "operator-4",
		SubjectType:   "user",
		ClusterID:     "prod",
		PermissionIDs: []string{"k8s.template:manage"},
	})
	router.ServeHTTP(scopedRecorder, scopedRequest)
	require.Equal(t, http.StatusBadRequest, scopedRecorder.Code)

	globalRecorder := httptest.NewRecorder()
	globalRequest := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "operator-4",
		SubjectType:   "user",
		Global:        true,
		PermissionIDs: []string{"k8s.template:manage"},
	})
	router.ServeHTTP(globalRecorder, globalRequest)
	require.Equal(t, http.StatusOK, globalRecorder.Code)
	decision := platformrbac.NewService(repo).Authorize(platformrbac.Subject{ID: "operator-4", Type: "user"}, platformrbac.Request{
		Resource: "k8s.template",
		Action:   "render",
		Scope:    platformrbac.Scope{Global: true},
	})
	require.True(t, decision.Allowed)
}

func TestCreateBindingCanGrantNamespaceManagement(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, _ := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "operator-5",
		SubjectType:   "user",
		ClusterID:     "prod",
		RiskAccepted:  true,
		PermissionIDs: []string{"k8s.namespace:manage"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	rbacSvc := platformrbac.NewService(repo)
	for _, action := range []string{"read", "create", "delete"} {
		decision := rbacSvc.Authorize(platformrbac.Subject{ID: "operator-5", Type: "user"}, platformrbac.Request{
			Resource: "k8s.namespace",
			Action:   action,
			Scope:    platformrbac.Scope{ClusterID: "prod"},
		})
		require.True(t, decision.Allowed, action)
	}
}

func TestCreateBindingCanGrantNamespaceReadWithoutManage(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, _ := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "operator-6",
		SubjectType:   "user",
		ClusterID:     "prod",
		PermissionIDs: []string{"k8s.namespace:read"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	rbacSvc := platformrbac.NewService(repo)
	read := rbacSvc.Authorize(platformrbac.Subject{ID: "operator-6", Type: "user"}, platformrbac.Request{
		Resource: "k8s.namespace",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: "prod"},
	})
	create := rbacSvc.Authorize(platformrbac.Subject{ID: "operator-6", Type: "user"}, platformrbac.Request{
		Resource: "k8s.namespace",
		Action:   "create",
		Scope:    platformrbac.Scope{ClusterID: "prod"},
	})
	require.True(t, read.Allowed)
	require.False(t, create.Allowed)
}

func TestCreateBindingGrantsNamespacePermissionsToMultipleNamespaces(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, auditStore := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "sre",
		SubjectType:   "group",
		ClusterID:     "prod",
		Namespaces:    []string{"orders", "payments"},
		PermissionIDs: []string{"k8s.resource:read"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Item  Binding   `json:"item"`
			Items []Binding `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Len(t, body.Data.Items, 1)
	require.ElementsMatch(t, []string{"orders", "payments"}, body.Data.Item.Scope.Namespaces)
	require.ElementsMatch(t, []string{"orders", "payments"}, body.Data.Items[0].Scope.Namespaces)

	rbacSvc := platformrbac.NewService(repo)
	for _, namespace := range []string{"orders", "payments"} {
		decision := rbacSvc.Authorize(platformrbac.Subject{ID: "sre", Type: "group"}, platformrbac.Request{
			Resource: "k8s.resource",
			Action:   "read",
			Scope:    platformrbac.Scope{ClusterID: "prod", Namespace: namespace},
		})
		require.True(t, decision.Allowed, namespace)
	}
	otherNamespace := rbacSvc.Authorize(platformrbac.Subject{ID: "sre", Type: "group"}, platformrbac.Request{
		Resource: "k8s.resource",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: "prod", Namespace: "billing"},
	})
	require.False(t, otherNamespace.Allowed)
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Contains(t, events[0].RequestSummary["namespaces"], "orders")
}

func TestCreateBindingAllNamespacesScopesNamespacePermissionsToCurrentCluster(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, _ := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "sre",
		SubjectType:   "group",
		ClusterID:     "prod",
		AllNamespaces: true,
		PermissionIDs: []string{"k8s.resource:read"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	rbacSvc := platformrbac.NewService(repo)
	for _, namespace := range []string{"orders", "payments"} {
		decision := rbacSvc.Authorize(platformrbac.Subject{ID: "sre", Type: "group"}, platformrbac.Request{
			Resource: "k8s.resource",
			Action:   "read",
			Scope:    platformrbac.Scope{ClusterID: "prod", Namespace: namespace},
		})
		require.True(t, decision.Allowed, namespace)
	}
	otherCluster := rbacSvc.Authorize(platformrbac.Subject{ID: "sre", Type: "group"}, platformrbac.Request{
		Resource: "k8s.resource",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: "stage", Namespace: "orders"},
	})
	require.False(t, otherCluster.Allowed)
}

func TestCreateBindingAllNamespacesHighRiskRequiresRiskAcceptance(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, _ := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "sre",
		SubjectType:   "group",
		ClusterID:     "prod",
		AllNamespaces: true,
		PermissionIDs: []string{"k8s.terminal:exec"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "k8s_platform_risk_confirmation_required")

	acceptedRecorder := httptest.NewRecorder()
	acceptedRequest := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "sre",
		SubjectType:   "group",
		ClusterID:     "prod",
		AllNamespaces: true,
		RiskAccepted:  true,
		PermissionIDs: []string{"k8s.terminal:exec"},
	})
	router.ServeHTTP(acceptedRecorder, acceptedRequest)
	require.Equal(t, http.StatusOK, acceptedRecorder.Code)
}

func TestCreateBindingClusterWideHighRiskRequiresRiskAcceptance(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, _ := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "sre",
		SubjectType:   "group",
		ClusterID:     "prod",
		PermissionIDs: []string{"k8s.credential:manage"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "k8s_platform_risk_confirmation_required")

	acceptedRecorder := httptest.NewRecorder()
	acceptedRequest := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "sre",
		SubjectType:   "group",
		ClusterID:     "prod",
		RiskAccepted:  true,
		PermissionIDs: []string{"k8s.credential:manage"},
	})
	router.ServeHTTP(acceptedRecorder, acceptedRequest)
	require.Equal(t, http.StatusOK, acceptedRecorder.Code)
}

func TestCreateBindingRejectsGlobalScopeForNonGlobalPermissions(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, _ := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "sre",
		SubjectType:   "group",
		Global:        true,
		PermissionIDs: []string{"k8s.resource:read"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "invalid_request")
}

func TestCreateBindingRejectsUnknownSubject(t *testing.T) {
	router, _ := newPlatformAccessRouter(t, platformAccessAdminRepo(), platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "missing-user",
		SubjectType:   "user",
		ClusterID:     "prod",
		Namespace:     "orders",
		PermissionIDs: []string{"k8s.resource:read"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Contains(t, recorder.Body.String(), "k8s_platform_subject_not_found")
}

func TestPermissionOptionsIncludeKubeconfigExport(t *testing.T) {
	options := PermissionOptions()

	require.Contains(t, permissionOptionIDs(options), "k8s.kubeconfig:export")
	require.Contains(t, permissionOptionIDs(options), "k8s.namespace:read")
	require.Contains(t, permissionOptionIDs(options), "k8s.namespace:manage")
	require.Contains(t, permissionOptionIDs(options), "k8s.service-account:manage")
	require.Contains(t, permissionOptionIDs(options), "k8s.certificate:manage")
	require.Contains(t, permissionOptionIDs(options), "k8s.deploy:rollback")
	require.Contains(t, permissionOptionIDs(options), "k8s.cluster-rbac:manage")
	require.Contains(t, permissionOptionIDs(options), "k8s.template:manage")
}

func TestPermissionProfilesExposeUsableK8sPackages(t *testing.T) {
	router, _ := newPlatformAccessRouter(t, platformAccessAdminRepo(), platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/profiles", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "k8s-readonly")
	require.Contains(t, recorder.Body.String(), "k8s-release-operator")
	require.Contains(t, recorder.Body.String(), "k8s-cluster-admin")
	require.Contains(t, recorder.Body.String(), "k8s-template-admin")
	require.Contains(t, recorder.Body.String(), "k8s.namespace:read")
	require.Contains(t, recorder.Body.String(), `"scope_mode":"mixed"`)
	require.Contains(t, recorder.Body.String(), "recommended_subject_type")
}

func TestPermissionProfilesExpandAllPermissionIDs(t *testing.T) {
	for _, profile := range PermissionProfiles() {
		permissions, err := expandPermissionIDs(profile.PermissionIDs)
		require.NoError(t, err, profile.ID)
		require.NotEmpty(t, permissions, profile.ID)
	}
}

func TestListSubjectsIncludesSubjectsFromExistingBindings(t *testing.T) {
	router, _ := newPlatformAccessRouter(t, platformAccessAdminRepo(), platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/subjects", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "admin-1")
	require.Contains(t, recorder.Body.String(), "binding_refs")
}

func newPlatformAccessRouter(t *testing.T, repo *testPlatformAccessRepo, subject platformrbac.Subject) (*gin.Engine, *audit.MemoryStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	auditStore := audit.NewMemoryStore()
	subjectRepo := NewMemorySubjectRepository()
	for _, subject := range []SubjectRecord{
		{ID: "user:operator-1", SubjectID: "operator-1", SubjectType: "user", DisplayName: "operator-1"},
		{ID: "user:operator-2", SubjectID: "operator-2", SubjectType: "user", DisplayName: "operator-2"},
		{ID: "user:operator-3", SubjectID: "operator-3", SubjectType: "user", DisplayName: "operator-3"},
		{ID: "user:operator-4", SubjectID: "operator-4", SubjectType: "user", DisplayName: "operator-4"},
		{ID: "user:operator-5", SubjectID: "operator-5", SubjectType: "user", DisplayName: "operator-5"},
		{ID: "user:operator-6", SubjectID: "operator-6", SubjectType: "user", DisplayName: "operator-6"},
		{ID: "user:admin-1", SubjectID: "admin-1", SubjectType: "user", DisplayName: "admin-1"},
		{ID: "group:sre", SubjectID: "sre", SubjectType: "group", DisplayName: "SRE"},
	} {
		require.NoError(t, subjectRepo.SaveSubject(context.Background(), subject))
	}
	service := NewService(repo, platformrbac.NewService(repo), audit.NewService(auditStore), subjectRepo)
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(authctx.WithSubject(ctx.Request.Context(), subject))
		ctx.Next()
	})
	router.GET("/bindings", ListBindingsHandler(service))
	router.POST("/bindings", CreateBindingHandler(service))
	router.DELETE("/bindings/:id", DeleteBindingHandler(service))
	router.GET("/permissions", PermissionsHandler(service))
	router.GET("/profiles", ProfilesHandler(service))
	router.GET("/subjects", ListSubjectsHandler(service))
	return router, auditStore
}

func newJSONRequest(method string, path string, body any) *http.Request {
	payload, _ := json.Marshal(body)
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func permissionOptionIDs(options []PermissionOption) []string {
	out := make([]string, 0, len(options))
	for _, option := range options {
		out = append(out, option.ID)
	}
	return out
}

func platformAccessAdminRepo() *testPlatformAccessRepo {
	now := time.Now().UTC()
	repo := &testPlatformAccessRepo{roles: map[string]platformrbac.Role{}}
	_ = repo.SaveRole(platformrbac.Role{
		ID:   "role-platform-access-admin",
		Name: "平台 K8s 授权管理员",
		Permissions: []platformrbac.Permission{
			{Resource: "k8s.platform-access", Action: "manage", ScopeMode: "global"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = repo.SaveBinding(platformrbac.Binding{
		ID:          "binding-platform-access-admin",
		SubjectID:   "admin-1",
		SubjectType: "user",
		RoleID:      "role-platform-access-admin",
		Scope:       platformrbac.Scope{Global: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	return repo
}

func platformAccessReaderRepo() *testPlatformAccessRepo {
	now := time.Now().UTC()
	repo := &testPlatformAccessRepo{roles: map[string]platformrbac.Role{}}
	_ = repo.SaveRole(platformrbac.Role{
		ID:   "role-reader",
		Name: "资源只读",
		Permissions: []platformrbac.Permission{
			{Resource: "k8s.resource", Action: "read", ScopeMode: "namespace"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = repo.SaveBinding(platformrbac.Binding{
		ID:          "binding-reader",
		SubjectID:   "reader-1",
		SubjectType: "user",
		RoleID:      "role-reader",
		Scope:       platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	return repo
}

type testPlatformAccessRepo struct {
	roles    map[string]platformrbac.Role
	bindings []platformrbac.Binding
}

func (r *testPlatformAccessRepo) SaveRole(role platformrbac.Role) error {
	r.roles[role.ID] = role
	return nil
}

func (r *testPlatformAccessRepo) GetRole(id string) (platformrbac.Role, error) {
	role, ok := r.roles[id]
	if !ok {
		return platformrbac.Role{}, ErrBindingNotFound
	}
	return role, nil
}

func (r *testPlatformAccessRepo) SaveBinding(binding platformrbac.Binding) error {
	for index, item := range r.bindings {
		if item.ID == binding.ID {
			r.bindings[index] = binding
			return nil
		}
	}
	r.bindings = append(r.bindings, binding)
	return nil
}

func (r *testPlatformAccessRepo) ListBindings() ([]platformrbac.Binding, error) {
	out := make([]platformrbac.Binding, len(r.bindings))
	copy(out, r.bindings)
	return out, nil
}

func (r *testPlatformAccessRepo) ListBindingsBySubject(subjectID string, subjectType string) ([]platformrbac.Binding, error) {
	out := make([]platformrbac.Binding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		if binding.SubjectID == subjectID && binding.SubjectType == subjectType {
			out = append(out, binding)
		}
	}
	return out, nil
}

func (r *testPlatformAccessRepo) DeleteBinding(id string) error {
	out := make([]platformrbac.Binding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		if binding.ID != id {
			out = append(out, binding)
		}
	}
	r.bindings = out
	return nil
}
