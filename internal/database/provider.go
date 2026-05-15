package database

import "context"

// Store is the top-level database abstraction.
type Store interface {
	Services() ServiceStore
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
	AlertRules() AlertRuleStore
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

// AlertRuleStore manages alert rules.
type AlertRuleStore interface {
	Insert(ctx context.Context, rule interface{}) error
	FindAll(ctx context.Context, results interface{}) error
	Count(ctx context.Context) (int64, error)
}
