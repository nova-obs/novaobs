package k8sops

import (
	"novaobs/internal/modules/k8sops/certificate"
	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/modules/k8sops/dashboard"
	"novaobs/internal/modules/k8sops/deployment"
	"novaobs/internal/modules/k8sops/kubeclient"
	"novaobs/internal/modules/k8sops/kubeconfig"
	"novaobs/internal/modules/k8sops/namespace"
	"novaobs/internal/modules/k8sops/platformaccess"
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
	ClusterCaps    cluster.CapabilityService
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
	PlatformAccess platformaccess.Service
}

func NewModule() Module {
	return NewModuleWithSecurity(nil, nil, nil)
}

func NewModuleWithSecurity(authorizer serviceaccount.Authorizer, auditor serviceaccount.Auditor, secrets kubeconfig.SecretService, dependencies ...any) Module {
	if secrets == nil {
		secrets = secret.NewService(secret.NewMemoryRepository(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	}
	clusterRepo := cluster.Repository(cluster.NewMemoryRepository(nil))
	clusterCapabilityProvider := cluster.CapabilityProvider(nil)
	namespaceRepo := namespace.Repository(namespace.NewMemoryRepository(nil))
	dashboardReader := dashboard.Reader(dashboard.NewStaticReader())
	clusterCredentialService := cluster.NewCredentialService(secrets, authorizer, auditor)
	serviceAccountRepo := serviceaccount.Repository(serviceaccount.NewMemoryRepository(nil))
	rbacRepo := k8srbac.Repository(k8srbac.NewMemoryRepository(nil, nil))
	certificateRepo := certificate.Repository(certificate.NewMemoryRepository(nil))
	resourceReader := resource.Reader(resource.NewMemoryReader(nil))
	platformAccessRepo := platformaccess.Repository(platformaccess.NewMemoryRepository())
	platformSubjectRepo := platformaccess.SubjectRepository(platformaccess.NewMemorySubjectRepository())
	terminalDependencies := []any{authorizer, auditor}
	deploymentReader := deployment.Reader(deployment.NewMemoryReader(nil))
	deploymentInventoryRepo := deployment.InventoryRepository(deployment.NewMemoryInventoryRepository(nil))
	deploymentDependencies := []any{authorizer, auditor, deploymentInventoryRepo}
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
		case deployment.InventoryRepository:
			if value != nil {
				deploymentInventoryRepo = value
				deploymentDependencies = append(deploymentDependencies, value)
			}
		case deployment.Reader:
			if value != nil {
				deploymentReader = value
				deploymentDependencies = append(deploymentDependencies, value)
			}
		case platformaccess.Repository:
			if value != nil {
				platformAccessRepo = value
			}
		case platformaccess.SubjectRepository:
			if value != nil {
				platformSubjectRepo = value
			}
		case kubeclient.ClientsetProvider:
			if value != nil {
				dashboardReader = dashboard.NewKubernetesReader(value, authorizer)
				namespaceRepo = namespace.NewKubernetesRepository(value, authorizer)
				resourceReader = resource.NewKubernetesReader(value, authorizer)
				serviceAccountRepo = serviceaccount.NewKubernetesRepository(value, authorizer)
				rbacRepo = k8srbac.NewKubernetesRepository(value, authorizer)
				certificateRepo = certificate.NewKubernetesRepository(value, authorizer)
				if provider, ok := value.(cluster.CapabilityProvider); ok {
					clusterCapabilityProvider = provider
					deploymentDependencies = append(deploymentDependencies, provider)
				}
				if provider, ok := value.(kubeclient.BundleProvider); ok {
					deploymentDependencies = append(deploymentDependencies, kubeclient.NewProviderBackedResourceOperationEngine(provider))
				}
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
		Dashboard:      dashboard.NewService(dashboardReader),
		Cluster:        cluster.NewService(clusterRepo),
		ClusterCaps:    cluster.NewCapabilityService(clusterCapabilityProvider),
		ClusterCred:    clusterCredentialService,
		Namespace:      namespace.NewService(namespaceRepo),
		Resource:       resource.NewService(resourceReader),
		Deploy:         deployment.NewService(deploymentReader, deploymentDependencies...),
		Cert:           certificate.NewService(certificateRepo, authorizer, auditor, secrets),
		ServiceAccount: serviceaccount.NewService(serviceAccountRepo, authorizer, auditor),
		RBAC:           k8srbac.NewService(rbacRepo, authorizer, auditor),
		Kubeconfig:     kubeconfig.NewService(secrets, authorizer, auditor),
		Template:       k8stemplate.NewService(k8stemplate.NewMemoryRepository(nil), authorizer, auditor),
		Terminal:       terminal.NewService(terminalDependencies...),
		PlatformAccess: platformaccess.NewService(platformAccessRepo, authorizer, auditor, platformSubjectRepo),
	}
}
