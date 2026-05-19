package k8sops

import (
	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/modules/k8sops/dashboard"
)

type Module struct {
	Dashboard dashboard.Service
	Cluster   cluster.Service
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
	}
}
