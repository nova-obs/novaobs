package deployment

import (
	"context"
	"testing"

	"novaobs/internal/modules/k8sops/kubeclient"
	"novaobs/internal/platform/audit"
	platformrbac "novaobs/internal/platform/rbac"

	"github.com/stretchr/testify/require"
)

func TestServiceListsDeploymentHistory(t *testing.T) {
	svc := NewService(NewMemoryReader([]HistoryRecord{
		{ID: "deploy-1", ClusterID: "prod", Namespace: "orders", Workload: "orders-api", Action: "rollout", Status: "warning"},
	}))

	items, err := svc.ListHistory(context.Background(), ListFilter{ClusterID: "prod"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "orders-api", items[0].Workload)
}

func TestServiceListsAuditEvents(t *testing.T) {
	svc := NewService(NewMemoryReader(nil, []AuditEvent{
		{ID: "audit-1", ClusterID: "prod", Namespace: "orders", ResourceKind: "Deployment", ResourceName: "orders-api", Action: "rollout.pause", Actor: "platform-admin"},
	}))

	items, err := svc.ListAuditEvents(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "rollout.pause", items[0].Action)
}

func TestServiceListsPlatformAuditEvents(t *testing.T) {
	auditSvc := audit.NewService(audit.NewMemoryStore())
	_, err := auditSvc.Record(context.Background(), audit.Event{
		Actor:        audit.Actor{ID: "user-1", Name: "alice"},
		Resource:     audit.Resource{Type: "k8s.terminal", Name: "orders"},
		ResourceType: "k8s.terminal",
		ResourceName: "orders",
		Action:       "exec",
		Scope:        "cluster=prod namespace=orders",
		RequestSummary: map[string]any{
			"cluster_id": "prod",
			"namespace":  "orders",
			"verb":       "delete",
		},
		Result: "blocked",
		Trace:  "trace-terminal-1",
	})
	require.NoError(t, err)

	svc := NewService(NewMemoryReader(nil), auditSvc)
	items, err := svc.ListAuditEvents(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders", Query: "exec"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "exec", items[0].Action)
	require.Equal(t, "alice", items[0].Actor)
	require.Equal(t, "blocked", items[0].Status)
	require.Equal(t, "trace-terminal-1", items[0].TraceID)
	require.Equal(t, "terminal", items[0].ResourceKind)
}

func TestServicePreviewResolvesResourceVersionFromClusterCapabilities(t *testing.T) {
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()), staticDeploymentCapabilityProvider{
		snapshot: kubeclient.CapabilitySnapshot{
			Resources: []kubeclient.APIResource{
				{Group: "extensions", Version: "v1beta1", GroupVersion: "extensions/v1beta1", Resource: "ingresses", Kind: "Ingress", Namespaced: true},
			},
		},
	})

	result, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: orders-ingress
  namespace: orders`,
	})

	require.NoError(t, err)
	require.Len(t, result.Resources, 1)
	require.Equal(t, "extensions/v1beta1", result.Resources[0].APIVersion)
	require.Equal(t, "Ingress", result.Resources[0].Kind)
}

func TestServicePreviewRunsServerSideDryRunAfterAuthorization(t *testing.T) {
	dryRunner := &recordingDeploymentDryRunner{result: kubeclient.DryRunApplyResult{
		Objects: []kubeclient.OperationObject{{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Namespace:  "orders",
			Name:       "orders-api",
		}},
	}}
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()), dryRunner)

	result, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: orders-api
  namespace: orders`,
	})

	require.NoError(t, err)
	require.Equal(t, 1, dryRunner.calls)
	require.Equal(t, "prod", dryRunner.request.ClusterID)
	require.Len(t, result.Resources, 1)
	require.Equal(t, "apps/v1", result.Resources[0].APIVersion)
	require.Equal(t, "Deployment", result.Resources[0].Kind)
	require.Equal(t, "orders", result.Resources[0].Namespace)
}

func TestServicePreviewRechecksPermissionForDryRunResultIdentities(t *testing.T) {
	dryRunner := &recordingDeploymentDryRunner{result: kubeclient.DryRunApplyResult{
		Objects: []kubeclient.OperationObject{{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Namespace:  "payments",
			Name:       "orders-api",
		}},
	}}
	svc := NewService(NewMemoryReader(nil), namespaceDeploymentAuthorizer{allowedNamespace: "orders"}, audit.NewService(audit.NewMemoryStore()), dryRunner)

	_, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: orders-api
  namespace: orders`,
	})

	require.ErrorIs(t, err, ErrPermissionDenied)
	require.Equal(t, 1, dryRunner.calls)
}

func TestServicePreviewRejectsUnsupportedResourceVersion(t *testing.T) {
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()), staticDeploymentCapabilityProvider{})

	_, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: orders-vs
  namespace: orders`,
	})

	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestServicePreviewChecksPermissionBeforeResolvingClusterCapabilities(t *testing.T) {
	provider := &countingDeploymentCapabilityProvider{}
	dryRunner := &recordingDeploymentDryRunner{}
	svc := NewService(NewMemoryReader(nil), denyDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()), provider, dryRunner)

	_, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: orders-vs
  namespace: orders`,
	})

	require.ErrorIs(t, err, ErrPermissionDenied)
	require.Equal(t, 0, provider.calls)
	require.Equal(t, 0, dryRunner.calls)
}

type staticDeploymentCapabilityProvider struct {
	snapshot kubeclient.CapabilitySnapshot
}

func (p staticDeploymentCapabilityProvider) Capabilities(_ context.Context, clusterID string) (kubeclient.CapabilitySnapshot, error) {
	p.snapshot.ClusterID = clusterID
	return p.snapshot, nil
}

type allowDeploymentAuthorizer struct{}

func (allowDeploymentAuthorizer) Authorize(platformrbac.Subject, platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: true}
}

type denyDeploymentAuthorizer struct{}

func (denyDeploymentAuthorizer) Authorize(platformrbac.Subject, platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false}
}

type namespaceDeploymentAuthorizer struct {
	allowedNamespace string
}

func (a namespaceDeploymentAuthorizer) Authorize(_ platformrbac.Subject, req platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: req.Scope.Namespace == a.allowedNamespace}
}

type countingDeploymentCapabilityProvider struct {
	calls int
}

func (p *countingDeploymentCapabilityProvider) Capabilities(context.Context, string) (kubeclient.CapabilitySnapshot, error) {
	p.calls++
	return kubeclient.CapabilitySnapshot{}, nil
}

type recordingDeploymentDryRunner struct {
	result  kubeclient.DryRunApplyResult
	request kubeclient.ClusterDryRunApplyRequest
	calls   int
}

func (r *recordingDeploymentDryRunner) DryRunApply(_ context.Context, req kubeclient.ClusterDryRunApplyRequest) (kubeclient.DryRunApplyResult, error) {
	r.calls++
	r.request = req
	return r.result, nil
}
