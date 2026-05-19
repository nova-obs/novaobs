package k8sops

import (
	"novaobs/internal/modules/k8sops/certificate"
	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/modules/k8sops/dashboard"
	"novaobs/internal/modules/k8sops/deployment"
	"novaobs/internal/modules/k8sops/kubeconfig"
	"novaobs/internal/modules/k8sops/namespace"
	k8srbac "novaobs/internal/modules/k8sops/rbac"
	"novaobs/internal/modules/k8sops/resource"
	"novaobs/internal/modules/k8sops/serviceaccount"
	"novaobs/internal/platform/secret"
)

type Module struct {
	Dashboard      dashboard.Service
	Cluster        cluster.Service
	Namespace      namespace.Service
	Resource       resource.Service
	Deploy         deployment.Service
	Cert           certificate.Service
	ServiceAccount serviceaccount.Service
	RBAC           k8srbac.Service
	Kubeconfig     kubeconfig.Service
}

func NewModule() Module {
	return NewModuleWithSecurity(nil, nil, nil)
}

func NewModuleWithSecurity(authorizer serviceaccount.Authorizer, auditor serviceaccount.Auditor, secrets kubeconfig.SecretService) Module {
	if secrets == nil {
		secrets = secret.NewService(secret.NewMemoryRepository(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	}
	return Module{
		Dashboard: dashboard.NewService(dashboard.NewStaticReader()),
		Cluster: cluster.NewService(cluster.NewMemoryRepository([]cluster.Cluster{
			{
				ID:          "prod",
				Name:        "prod-core",
				Version:     "v1.29.4",
				Region:      "cn-shanghai",
				Description: "生产核心集群，来自 startorch 集群清单基线",
				Status:      "active",
			},
		})),
		Namespace: namespace.NewService(namespace.NewMemoryRepository([]namespace.Namespace{
			{ID: "orders", ClusterID: "prod", Name: "orders", Status: "active", Owner: "orders-team", Phase: "Active"},
			{ID: "payment", ClusterID: "prod", Name: "payment", Status: "active", Owner: "payment-team", Phase: "Active"},
		})),
		Resource: resource.NewService(resource.NewMemoryReader([]resource.ResourceSummary{
			{
				Identity: resource.Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "Deployment", Name: "orders-api", UID: "uid-orders-api"},
				Status:   "warning",
				Labels:   map[string]string{"app": "orders-api"},
			},
			{
				Identity: resource.Identity{ClusterID: "prod", Namespace: "payment", APIVersion: "apps/v1", Kind: "Deployment", Name: "payment-gateway", UID: "uid-payment-gateway"},
				Status:   "healthy",
				Labels:   map[string]string{"app": "payment-gateway"},
			},
		})),
		Deploy: deployment.NewService(deployment.NewMemoryReader([]deployment.HistoryRecord{
			{ID: "deploy-orders-1", ClusterID: "prod", Namespace: "orders", Workload: "orders-api", Action: "rollout.pause", Status: "warning", Revision: "rev-1842", Actor: "platform-admin"},
		}, []deployment.AuditEvent{
			{ID: "audit-orders-1", ClusterID: "prod", Namespace: "orders", ResourceKind: "Deployment", ResourceName: "orders-api", Action: "rollout.pause", Actor: "platform-admin", Status: "warning", TraceID: "trace-k8s-1842"},
		})),
		Cert: certificate.NewService(certificate.NewMemoryRepository([]certificate.Certificate{
			{ID: "cert-prod-1", ClusterID: "prod", Namespace: "ingress", Name: "wildcard-prod", CommonName: "*.prod.example.com", Fingerprint: "sha256:6f7d8e", Status: "valid", Source: "startorch"},
		})),
		ServiceAccount: serviceaccount.NewService(serviceaccount.NewMemoryRepository([]serviceaccount.ServiceAccount{
			{ID: "sa-prod-orders-reader", ClusterID: "prod", Namespace: "orders", Name: "orders-reader", UID: "uid-orders-reader", Status: "active", Source: "startorch"},
		}), authorizer, auditor),
		RBAC: k8srbac.NewService(k8srbac.NewMemoryRepository([]k8srbac.RoleResource{
			{ID: "role-prod-orders-reader", ClusterID: "prod", Namespace: "orders", Kind: "Role", Name: "orders-reader", UID: "uid-role-orders-reader", Rules: []k8srbac.Rule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}}}, Source: "startorch"},
		}, []k8srbac.BindingResource{
			{ID: "binding-prod-orders-reader", ClusterID: "prod", Namespace: "orders", Kind: "RoleBinding", Name: "orders-reader-binding", UID: "uid-binding-orders-reader", RoleRef: k8srbac.RoleRef{Kind: "Role", Name: "orders-reader"}, Subjects: []k8srbac.Subject{{Kind: "ServiceAccount", Name: "orders-reader", Namespace: "orders"}}, Source: "startorch"},
		}), authorizer, auditor),
		Kubeconfig: kubeconfig.NewService(secrets, authorizer, auditor),
	}
}
