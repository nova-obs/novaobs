package k8sops

import (
	"novaobs/internal/modules/k8sops/certificate"
	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/modules/k8sops/dashboard"
	"novaobs/internal/modules/k8sops/deployment"
	"novaobs/internal/modules/k8sops/kubeclient"
	"novaobs/internal/modules/k8sops/kubeconfig"
	"novaobs/internal/modules/k8sops/namespace"
	k8srbac "novaobs/internal/modules/k8sops/rbac"
	"novaobs/internal/modules/k8sops/resource"
	"novaobs/internal/modules/k8sops/serviceaccount"
	k8stemplate "novaobs/internal/modules/k8sops/template"
	"novaobs/internal/modules/k8sops/terminal"
	"novaobs/internal/platform/secret"
)

type Module struct {
	Dashboard      dashboard.Service
	Cluster        cluster.Service
	ClusterCred    cluster.CredentialService
	Namespace      namespace.Service
	Resource       resource.Service
	Deploy         deployment.Service
	Cert           certificate.Service
	ServiceAccount serviceaccount.Service
	RBAC           k8srbac.Service
	Kubeconfig     kubeconfig.Service
	Template       k8stemplate.Service
	Terminal       terminal.Service
}

func NewModule() Module {
	return NewModuleWithSecurity(nil, nil, nil)
}

func NewModuleWithSecurity(authorizer serviceaccount.Authorizer, auditor serviceaccount.Auditor, secrets kubeconfig.SecretService, dependencies ...any) Module {
	if secrets == nil {
		secrets = secret.NewService(secret.NewMemoryRepository(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	}
	clusterRepo := cluster.Repository(cluster.NewMemoryRepository(nil))
	namespaceRepo := namespace.Repository(namespace.NewMemoryRepository([]namespace.Namespace{
		{ID: "orders", ClusterID: "prod", Name: "orders", Status: "active", Owner: "orders-team", Phase: "Active"},
		{ID: "payment", ClusterID: "prod", Name: "payment", Status: "active", Owner: "payment-team", Phase: "Active"},
	}))
	dashboardReader := dashboard.Reader(dashboard.NewStaticReader())
	clusterCredentialService := cluster.NewCredentialService(secrets, authorizer, auditor)
	serviceAccountRepo := serviceaccount.Repository(serviceaccount.NewMemoryRepository([]serviceaccount.ServiceAccount{
		{ID: "sa-prod-orders-reader", ClusterID: "prod", Namespace: "orders", Name: "orders-reader", UID: "uid-orders-reader", Status: "active", Source: "startorch"},
	}))
	rbacRepo := k8srbac.Repository(k8srbac.NewMemoryRepository([]k8srbac.RoleResource{
		{ID: "role-prod-orders-reader", ClusterID: "prod", Namespace: "orders", Kind: "Role", Name: "orders-reader", UID: "uid-role-orders-reader", Rules: []k8srbac.Rule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}}}, Source: "startorch"},
	}, []k8srbac.BindingResource{
		{ID: "binding-prod-orders-reader", ClusterID: "prod", Namespace: "orders", Kind: "RoleBinding", Name: "orders-reader-binding", UID: "uid-binding-orders-reader", RoleRef: k8srbac.RoleRef{Kind: "Role", Name: "orders-reader"}, Subjects: []k8srbac.Subject{{Kind: "ServiceAccount", Name: "orders-reader", Namespace: "orders"}}, Source: "startorch"},
	}))
	certificateRepo := certificate.Repository(certificate.NewMemoryRepository([]certificate.Certificate{
		{ID: "cert-prod-1", ClusterID: "prod", Namespace: "ingress", Name: "wildcard-prod", CommonName: "*.prod.example.com", Fingerprint: "sha256:6f7d8e", Status: "valid", Source: "startorch"},
	}))
	resourceReader := resource.Reader(resource.NewMemoryReader([]resource.ResourceSummary{
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
	}))
	terminalDependencies := []any{authorizer, auditor}
	for _, dependency := range dependencies {
		switch value := dependency.(type) {
		case cluster.Repository:
			if value != nil {
				clusterRepo = value
			}
		case namespace.Repository:
			if value != nil {
				namespaceRepo = value
			}
		case resource.Reader:
			if value != nil {
				resourceReader = value
			}
		case kubeclient.ClientsetProvider:
			if value != nil {
				dashboardReader = dashboard.NewKubernetesReader(value, authorizer)
				namespaceRepo = namespace.NewKubernetesRepository(value, authorizer)
				resourceReader = resource.NewKubernetesReader(value, authorizer)
				serviceAccountRepo = serviceaccount.NewKubernetesRepository(value, authorizer)
				rbacRepo = k8srbac.NewKubernetesRepository(value, authorizer)
				certificateRepo = certificate.NewKubernetesRepository(value, authorizer)
			}
		case terminal.Executor:
			if value != nil {
				terminalDependencies = append(terminalDependencies, value)
			}
		case terminal.CommandPolicy:
			terminalDependencies = append(terminalDependencies, value)
		}
	}
	return Module{
		Dashboard:   dashboard.NewService(dashboardReader),
		Cluster:     cluster.NewService(clusterRepo),
		ClusterCred: clusterCredentialService,
		Namespace:   namespace.NewService(namespaceRepo),
		Resource:    resource.NewService(resourceReader),
		Deploy: deployment.NewService(deployment.NewMemoryReader([]deployment.HistoryRecord{
			{ID: "deploy-orders-1", ClusterID: "prod", Namespace: "orders", Workload: "orders-api", Action: "rollout.pause", Status: "warning", Revision: "rev-1842", Actor: "platform-admin"},
		}, []deployment.AuditEvent{
			{ID: "audit-orders-1", ClusterID: "prod", Namespace: "orders", ResourceKind: "Deployment", ResourceName: "orders-api", Action: "rollout.pause", Actor: "platform-admin", Status: "warning", TraceID: "trace-k8s-1842"},
		}), authorizer, auditor),
		Cert:           certificate.NewService(certificateRepo, authorizer, auditor, secrets),
		ServiceAccount: serviceaccount.NewService(serviceAccountRepo, authorizer, auditor),
		RBAC:           k8srbac.NewService(rbacRepo, authorizer, auditor),
		Kubeconfig:     kubeconfig.NewService(secrets, authorizer, auditor),
		Template: k8stemplate.NewService(k8stemplate.NewMemoryRepository([]k8stemplate.Template{
			{
				ID:          "tpl-orders-deployment",
				Name:        "orders-deployment",
				Type:        "Deployment",
				YAMLContent: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: <<name>>\n  namespace: <<namespace>>\nspec:\n  replicas: <<replicas>>\n",
				Variables: []k8stemplate.Variable{
					{Name: "name", Required: true},
					{Name: "namespace", Required: true},
					{Name: "replicas", DefaultValue: "2"},
				},
				Description: "来自 startorch deployment 基线的 NovaObs 模板",
				Source:      "startorch",
			},
		}), authorizer, auditor),
		Terminal: terminal.NewService(terminalDependencies...),
	}
}
