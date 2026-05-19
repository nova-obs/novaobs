package mongo

import (
	"context"
	"fmt"
	"time"

	"novaobs/internal/database"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const dbName = "observability_platform"

type Store struct {
	client *mongo.Client
	db     *mongo.Database
	svcCol *mongo.Collection
	stgCol *mongo.Collection
	cgCol  *mongo.Collection
	ciCol  *mongo.Collection
	ccvCol *mongo.Collection
	cptCol *mongo.Collection
	cgoCol *mongo.Collection
	sepCol *mongo.Collection
	sprCol *mongo.Collection
	sppCol *mongo.Collection
	iiCol  *mongo.Collection
	onbCol *mongo.Collection
	arCol  *mongo.Collection
	rrCol  *mongo.Collection
	rbCol  *mongo.Collection
}

func NewStore(ctx context.Context, uri string) (*Store, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri).
		SetConnectTimeout(5*time.Second).
		SetServerSelectionTimeout(5*time.Second))
	if err != nil {
		return nil, fmt.Errorf("连接 MongoDB 失败: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("Ping MongoDB 失败: %w", err)
	}
	db := client.Database(dbName)
	return &Store{
		client: client,
		db:     db,
		svcCol: db.Collection("services"),
		stgCol: db.Collection("service_targets"),
		cgCol:  db.Collection("collector_groups"),
		ciCol:  db.Collection("collector_instances"),
		ccvCol: db.Collection("collector_config_versions"),
		cptCol: db.Collection("collector_platform_templates"),
		cgoCol: db.Collection("collector_group_overrides"),
		sepCol: db.Collection("service_enrichment_patches"),
		sprCol: db.Collection("service_parser_rules"),
		sppCol: db.Collection("service_pipeline_patches"),
		iiCol:  db.Collection("ingestion_identities"),
		onbCol: db.Collection("service_onboardings"),
		arCol:  db.Collection("alert_rules"),
		rrCol:  db.Collection("rbac_roles"),
		rbCol:  db.Collection("rbac_bindings"),
	}, nil
}

func (s *Store) Close(ctx context.Context) error {
	return s.client.Disconnect(ctx)
}

// ---------- sub-store accessors ----------

func (s *Store) Services() database.ServiceStore                     { return &svcStore{s.svcCol} }
func (s *Store) ServiceTargets() database.ServiceTargetStore         { return &targetStore{s.stgCol} }
func (s *Store) CollectorGroups() database.CollectorGroupStore       { return &cgStore{s.cgCol} }
func (s *Store) CollectorInstances() database.CollectorInstanceStore { return &ciStore{s.ciCol} }
func (s *Store) CollectorConfigVersions() database.CollectorConfigVersionStore {
	return &ccvStore{s.ccvCol}
}
func (s *Store) CollectorPlatformTemplates() database.CollectorPlatformTemplateStore {
	return &cptStore{s.cptCol}
}
func (s *Store) CollectorGroupOverrides() database.CollectorGroupOverrideStore {
	return &cgoStore{s.cgoCol}
}
func (s *Store) ServiceEnrichmentPatches() database.ServiceEnrichmentPatchStore {
	return &serviceScopedStore{s.sepCol}
}
func (s *Store) ServiceParserRules() database.ServiceParserRuleStore {
	return &serviceScopedStore{s.sprCol}
}
func (s *Store) ServicePipelinePatches() database.ServicePipelinePatchStore {
	return &serviceScopedStore{s.sppCol}
}
func (s *Store) IngestionIdentities() database.IngestionIdentityStore { return &iiStore{s.iiCol} }
func (s *Store) Onboardings() database.OnboardingStore                { return &onbStore{s.onbCol} }
func (s *Store) AlertRules() database.AlertRuleStore                  { return &arStore{s.arCol} }
func (s *Store) RBACRoles() database.RBACRoleStore                    { return &rbacRoleStore{s.rrCol} }
func (s *Store) RBACBindings() database.RBACBindingStore              { return &rbacBindingStore{s.rbCol} }

// ---------- helpers ----------

func objectID(id string) (interface{}, error) {
	return id, nil
}
