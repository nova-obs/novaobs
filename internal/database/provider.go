package database

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("database_not_found")
	ErrConflict = errors.New("database_conflict")
)

// Store is the top-level database abstraction.
type Store interface {
	Products() ProductStore
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
	VMLogAgentEndpoints() VMLogAgentEndpointStore
	LogTargets() LogTargetStore
	LogCollectorConfigVersions() LogCollectorConfigVersionStore
	LogDeploymentManifestVersions() LogDeploymentManifestVersionStore
	LogCollectorClusterConfigs() LogCollectorClusterConfigStore
	ObservabilityRuntimes() ObservabilityRuntimeStore
	MetricsIntegrations() MetricsIntegrationStore
	MetricsSourceAccesses() MetricsSourceAccessStore
	MetricsHealthSnapshots() MetricsHealthSnapshotStore
	MetricsCollectorReleases() MetricsCollectorReleaseStore
	Alerting() AlertingStore
	RBACRoles() RBACRoleStore
	RBACBindings() RBACBindingStore
	PlatformSubjects() PlatformSubjectStore
	IAMUsers() IAMUserStore
	IAMGroups() IAMGroupStore
	IAMMemberships() IAMMembershipStore
	IAMServiceAccounts() IAMServiceAccountStore
	PlatformImages() PlatformImageStore
	Secrets() SecretStore
	AuditEvents() AuditEventStore
	Environments() EnvironmentStore
	EnvironmentResourceBindings() EnvironmentResourceBindingStore
	K8sClusters() K8sClusterStore
	K8sNamespaces() K8sNamespaceStore
	K8sDeploymentInventory() K8sDeploymentInventoryStore
	K8sDeploymentHistory() K8sDeploymentHistoryStore
	Close(ctx context.Context) error
}

type ProductStore interface {
	Insert(ctx context.Context, product interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Update(ctx context.Context, id string, product interface{}) error
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
	FindByRuntimeIdentity(ctx context.Context, runtimeIdentity string, result interface{}) error
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
	FindByService(ctx context.Context, serviceID string, results interface{}) error
	FindByAgentGroup(ctx context.Context, agentGroupID string, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Update(ctx context.Context, id string, route interface{}) error
	Delete(ctx context.Context, id string) error
}

type VMLogAgentEndpointStore interface {
	Insert(ctx context.Context, endpoint interface{}) error
	FindByRoute(ctx context.Context, routeID string, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Update(ctx context.Context, id string, endpoint interface{}) error
	Delete(ctx context.Context, id string) error
	DeleteByRoute(ctx context.Context, routeID string) error
}

type LogTargetStore interface {
	Insert(ctx context.Context, target interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByService(ctx context.Context, serviceID string, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Update(ctx context.Context, id string, target interface{}) error
}

type LogCollectorConfigVersionStore interface {
	Upsert(ctx context.Context, hash string, version interface{}) error
	FindByHash(ctx context.Context, hash string, result interface{}) error
}

type LogDeploymentManifestVersionStore interface {
	Upsert(ctx context.Context, hash string, version interface{}) error
	FindByHash(ctx context.Context, hash string, result interface{}) error
}

type LogCollectorClusterConfigStore interface {
	Upsert(ctx context.Context, clusterID string, agentNamespace string, config interface{}) error
	FindByCluster(ctx context.Context, clusterID string, agentNamespace string, result interface{}) error
}

type ObservabilityRuntimeStore interface {
	Upsert(ctx context.Context, id string, runtime interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	FindByCluster(ctx context.Context, clusterID string, results interface{}) error
}

type MetricsIntegrationStore interface {
	Insert(ctx context.Context, integration interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	FindByEnvironment(ctx context.Context, environmentID string, result interface{}) error
	Update(ctx context.Context, id string, integration interface{}) error
	Delete(ctx context.Context, id string) error
}

type MetricsSourceAccessStore interface {
	Insert(ctx context.Context, source interface{}) error
	FindByIntegration(ctx context.Context, integrationID string, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Update(ctx context.Context, id string, source interface{}) error
	Delete(ctx context.Context, id string) error
}

type MetricsHealthSnapshotStore interface {
	Insert(ctx context.Context, snapshot interface{}) error
	FindLatestByIntegration(ctx context.Context, integrationID string, result interface{}) error
}

type MetricsCollectorReleaseStore interface {
	Insert(ctx context.Context, release interface{}) error
	Update(ctx context.Context, id string, release interface{}) error
	FindLatestBySourceAccess(ctx context.Context, sourceAccessID string, result interface{}) error
}

type AlertingStore interface {
	SaveChange(ctx context.Context, expectedCurrentUpdateID string, rule interface{}, update interface{}, auditEvent interface{}) error
	FindRules(ctx context.Context, serviceID string, state string, signalType string, results interface{}) error
	FindRuleByID(ctx context.Context, id string, result interface{}) error
	FindUpdate(ctx context.Context, ruleID string, updateID string, result interface{}) error
	FindUpdates(ctx context.Context, ruleID string, limit int, results interface{}) error
	FindRuntimeRules(ctx context.Context, endpointID string, signalType string, results interface{}) error
	MarkRuntimeRulesApplied(ctx context.Context, endpointID string, signalType string, appliedAt time.Time) (int64, error)
	ApplyAlertEvent(ctx context.Context, instance interface{}, event interface{}) error
	FindAlertInstances(ctx context.Context, ruleID string, serviceID string, state string, limit int, results interface{}) error
	FindAlertEvents(ctx context.Context, ruleID string, fingerprint string, limit int, results interface{}) error
	SaveNotificationPolicy(ctx context.Context, expectedUpdatedAt time.Time, policy interface{}, auditEvent interface{}) error
	FindNotificationPolicyByID(ctx context.Context, id string, result interface{}) error
	FindNotificationPolicies(ctx context.Context, serviceID string, enabledOnly bool, results interface{}) error
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

type PlatformImageStore interface {
	Upsert(ctx context.Context, key string, image interface{}) error
	FindAll(ctx context.Context, results interface{}) error
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

type EnvironmentStore interface {
	Insert(ctx context.Context, environment interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	FindByID(ctx context.Context, id string, result interface{}) error
	Update(ctx context.Context, id string, environment interface{}) error
}

type EnvironmentResourceBindingStore interface {
	Insert(ctx context.Context, binding interface{}) error
	FindByEnvironment(ctx context.Context, environmentID string, results interface{}) error
	FindByResource(ctx context.Context, resourceKind string, resourceRef string, result interface{}) error
	Delete(ctx context.Context, id string) error
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
