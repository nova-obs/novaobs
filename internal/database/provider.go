package database

import "context"

// Store is the top-level database abstraction.
type Store interface {
	Services() ServiceStore
	ServiceTargets() ServiceTargetStore
	CollectorGroups() CollectorGroupStore
	CollectorInstances() CollectorInstanceStore
	CollectorConfigVersions() CollectorConfigVersionStore
	CollectorPlatformTemplates() CollectorPlatformTemplateStore
	CollectorGroupOverrides() CollectorGroupOverrideStore
	ServiceEnrichmentPatches() ServiceEnrichmentPatchStore
	ServiceParserRules() ServiceParserRuleStore
	ServicePipelinePatches() ServicePipelinePatchStore
	IngestionIdentities() IngestionIdentityStore
	Onboardings() OnboardingStore
	LogEndpoints() LogEndpointStore
	LogSources() LogSourceStore
	LogRoutes() LogRouteStore
	LogAgentPlans() LogAgentPlanStore
	AlertRules() AlertRuleStore
	RBACRoles() RBACRoleStore
	RBACBindings() RBACBindingStore
	PlatformSubjects() PlatformSubjectStore
	IAMUsers() IAMUserStore
	IAMGroups() IAMGroupStore
	IAMMemberships() IAMMembershipStore
	IAMServiceAccounts() IAMServiceAccountStore
	Secrets() SecretStore
	AuditEvents() AuditEventStore
	K8sClusters() K8sClusterStore
	K8sNamespaces() K8sNamespaceStore
	K8sDeploymentInventory() K8sDeploymentInventoryStore
	K8sDeploymentHistory() K8sDeploymentHistoryStore
	Close(ctx context.Context) error
}

// ServiceStore manages service catalog entries.
type ServiceStore interface {
	Insert(ctx context.Context, svc interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Update(ctx context.Context, id string, svc interface{}) error
	Count(ctx context.Context) (int64, error)
}

// ServiceTargetStore manages observed runtime targets linked to services.
type ServiceTargetStore interface {
	Insert(ctx context.Context, target interface{}) error
	FindByService(ctx context.Context, serviceID string, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Update(ctx context.Context, id string, target interface{}) error
}

// CollectorGroupStore manages collector groups.
type CollectorGroupStore interface {
	Insert(ctx context.Context, group interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Update(ctx context.Context, id string, group interface{}) error
	Count(ctx context.Context) (int64, error)
}

// CollectorInstanceStore manages collector instances.
type CollectorInstanceStore interface {
	Upsert(ctx context.Context, instanceUID string, groupID string, instance interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByGroup(ctx context.Context, groupID string, results interface{}) error
	FindByUID(ctx context.Context, instanceUID string, result interface{}) error
	Update(ctx context.Context, instanceUID string, instance interface{}) error
	Delete(ctx context.Context, instanceUID string) error
}

// CollectorConfigVersionStore manages collector group config versions.
type CollectorConfigVersionStore interface {
	Insert(ctx context.Context, version interface{}) error
	FindByGroup(ctx context.Context, groupID string, results interface{}) error
	UpdateStatusByGroupAndHash(ctx context.Context, groupID string, configHash string, updates map[string]interface{}) error
}

type CollectorPlatformTemplateStore interface {
	Insert(ctx context.Context, template interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Update(ctx context.Context, id string, template interface{}) error
}

type CollectorGroupOverrideStore interface {
	Upsert(ctx context.Context, groupID string, override interface{}) error
	FindByGroup(ctx context.Context, groupID string, result interface{}) error
}

type ServiceEnrichmentPatchStore interface {
	Upsert(ctx context.Context, serviceID string, patch interface{}) error
	FindByService(ctx context.Context, serviceID string, result interface{}) error
	FindByCollectorGroup(ctx context.Context, groupID string, results interface{}) error
}

type ServiceParserRuleStore interface {
	Upsert(ctx context.Context, serviceID string, rule interface{}) error
	FindByService(ctx context.Context, serviceID string, result interface{}) error
	FindByCollectorGroup(ctx context.Context, groupID string, results interface{}) error
}

type ServicePipelinePatchStore interface {
	Upsert(ctx context.Context, serviceID string, patch interface{}) error
	FindByService(ctx context.Context, serviceID string, result interface{}) error
	FindByCollectorGroup(ctx context.Context, groupID string, results interface{}) error
}

// IngestionIdentityStore manages ingestion identities.
type IngestionIdentityStore interface {
	Insert(ctx context.Context, identity interface{}) error
	Upsert(ctx context.Context, serviceID string, identity interface{}) error
	FindByService(ctx context.Context, serviceID string, result interface{}) error
}

// OnboardingStore manages service onboarding state.
type OnboardingStore interface {
	Upsert(ctx context.Context, serviceID string, onboarding interface{}) error
	FindByService(ctx context.Context, serviceID string, result interface{}) error
	FindByCollectorGroup(ctx context.Context, groupID string, results interface{}) error
}

type LogEndpointStore interface {
	Insert(ctx context.Context, endpoint interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Update(ctx context.Context, id string, endpoint interface{}) error
}

type LogSourceStore interface {
	Upsert(ctx context.Context, id string, source interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
}

type LogRouteStore interface {
	Upsert(ctx context.Context, id string, route interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Update(ctx context.Context, id string, route interface{}) error
}

type LogAgentPlanStore interface {
	Insert(ctx context.Context, plan interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByRoute(ctx context.Context, routeID string, results interface{}) error
}

// AlertRuleStore manages alert rules.
type AlertRuleStore interface {
	Insert(ctx context.Context, rule interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	Count(ctx context.Context) (int64, error)
}

type RBACRoleStore interface {
	Upsert(ctx context.Context, id string, role interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Delete(ctx context.Context, id string) error
}

type RBACBindingStore interface {
	Upsert(ctx context.Context, id string, binding interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindBySubject(ctx context.Context, subjectID string, subjectType string, results interface{}) error
	Delete(ctx context.Context, id string) error
}

type PlatformSubjectStore interface {
	Upsert(ctx context.Context, id string, subject interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	Delete(ctx context.Context, id string) error
}

type IAMUserStore interface {
	Upsert(ctx context.Context, id string, user interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Delete(ctx context.Context, id string) error
}

type IAMGroupStore interface {
	Upsert(ctx context.Context, id string, group interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Delete(ctx context.Context, id string) error
}

type IAMMembershipStore interface {
	Upsert(ctx context.Context, id string, membership interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByGroup(ctx context.Context, groupID string, results interface{}) error
	FindBySubject(ctx context.Context, subjectID string, subjectType string, results interface{}) error
	Delete(ctx context.Context, id string) error
}

type IAMServiceAccountStore interface {
	Upsert(ctx context.Context, id string, serviceAccount interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Delete(ctx context.Context, id string) error
}

type SecretStore interface {
	Upsert(ctx context.Context, id string, secret interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	FindByTypeAndScope(ctx context.Context, typ string, scope interface{}, result interface{}) error
	FindByType(ctx context.Context, typ string, results interface{}) error
}

type AuditEventStore interface {
	Insert(ctx context.Context, event interface{}) error
	FindAll(ctx context.Context, results interface{}) error
}

type K8sClusterStore interface {
	Upsert(ctx context.Context, id string, cluster interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	Delete(ctx context.Context, id string) error
}

type K8sNamespaceStore interface {
	Upsert(ctx context.Context, id string, namespace interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByCluster(ctx context.Context, clusterID string, results interface{}) error
}

type K8sDeploymentInventoryStore interface {
	Upsert(ctx context.Context, id string, record interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByIdentity(ctx context.Context, clusterID string, namespace string, apiVersion string, kind string, name string, result interface{}) error
	Delete(ctx context.Context, id string) error
}

type K8sDeploymentHistoryStore interface {
	Insert(ctx context.Context, record interface{}) error
	FindAll(ctx context.Context, results interface{}) error
}
