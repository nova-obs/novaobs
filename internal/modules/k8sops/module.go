package k8sops

import (
	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/modules/k8sops/dashboard"
	"novaobs/internal/modules/k8sops/namespace"
)

type Module struct {
	Dashboard dashboard.Service
	Cluster   cluster.Service
	Namespace namespace.Service
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
	}
}
