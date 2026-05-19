package k8sops

import "novaobs/internal/modules/k8sops/dashboard"

type Module struct {
	Dashboard dashboard.Service
}

func NewModule() Module {
	return Module{
		Dashboard: dashboard.NewService(dashboard.NewStaticReader()),
	}
}
