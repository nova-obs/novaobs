package k8sops

import (
	"novaobs/internal/modules/k8sops/certificate"
	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/modules/k8sops/dashboard"
	"novaobs/internal/modules/k8sops/deployment"
	"novaobs/internal/modules/k8sops/namespace"
	"novaobs/internal/modules/k8sops/resource"
)

type Module struct {
	Dashboard dashboard.Service
	Cluster   cluster.Service
	Namespace namespace.Service
	Resource  resource.Service
	Deploy    deployment.Service
	Cert      certificate.Service
}

func NewModule() Module {
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
	}
}
