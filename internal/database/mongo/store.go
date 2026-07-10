package mongo

import (
	"context"
	"fmt"
	"time"

	"novaapm/internal/database"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const dbName = "observability_platform"

type Store struct {
	client  *mongo.Client
	db      *mongo.Database
	prdCol  *mongo.Collection
	svcCol  *mongo.Collection
	stgCol  *mongo.Collection
	cgCol   *mongo.Collection
	ciCol   *mongo.Collection
	ccvCol  *mongo.Collection
	cptCol  *mongo.Collection
	cgoCol  *mongo.Collection
	sepCol  *mongo.Collection
	sprCol  *mongo.Collection
	sppCol  *mongo.Collection
	iiCol   *mongo.Collection
	onbCol  *mongo.Collection
	lgeCol  *mongo.Collection
	lgsCol  *mongo.Collection
	lgrCol  *mongo.Collection
	lgtCol  *mongo.Collection
	lgcvCol *mongo.Collection
	lgdvCol *mongo.Collection
	lgccCol *mongo.Collection
	ortCol  *mongo.Collection
	msbCol  *mongo.Collection
	mrtCol  *mongo.Collection
	arCol   *mongo.Collection
	aruCol  *mongo.Collection
	ariCol  *mongo.Collection
	areCol  *mongo.Collection
	arpCol  *mongo.Collection
	rrCol   *mongo.Collection
	rbCol   *mongo.Collection
	psCol   *mongo.Collection
	iuCol   *mongo.Collection
	igCol   *mongo.Collection
	imCol   *mongo.Collection
	isaCol  *mongo.Collection
	imgCol  *mongo.Collection
	secCol  *mongo.Collection
	aeCol   *mongo.Collection
	kclCol  *mongo.Collection
	knsCol  *mongo.Collection
	kdiCol  *mongo.Collection
	kdhCol  *mongo.Collection
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
	store := &Store{
		client:  client,
		db:      db,
		prdCol:  db.Collection("products"),
		svcCol:  db.Collection("services"),
		stgCol:  db.Collection("service_targets"),
		cgCol:   db.Collection("collector_groups"),
		ciCol:   db.Collection("collector_instances"),
		ccvCol:  db.Collection("collector_config_versions"),
		cptCol:  db.Collection("collector_platform_templates"),
		cgoCol:  db.Collection("collector_group_overrides"),
		sepCol:  db.Collection("service_enrichment_patches"),
		sprCol:  db.Collection("service_parser_rules"),
		sppCol:  db.Collection("service_pipeline_patches"),
		iiCol:   db.Collection("ingestion_identities"),
		onbCol:  db.Collection("service_onboardings"),
		lgeCol:  db.Collection("log_endpoints"),
		lgsCol:  db.Collection("log_sources"),
		lgrCol:  db.Collection("log_routes"),
		lgtCol:  db.Collection("log_targets"),
		lgcvCol: db.Collection("log_collector_config_versions"),
		lgdvCol: db.Collection("log_deployment_manifest_versions"),
		lgccCol: db.Collection("log_collector_cluster_configs"),
		ortCol:  db.Collection("observability_runtimes"),
		msbCol:  db.Collection("metrics_service_bindings"),
		mrtCol:  db.Collection("metrics_routes"),
		arCol:   db.Collection("alert_rules"),
		aruCol:  db.Collection("alert_rule_updates"),
		ariCol:  db.Collection("alert_instances"),
		areCol:  db.Collection("alert_events"),
		arpCol:  db.Collection("alert_notification_policies"),
		rrCol:   db.Collection("rbac_roles"),
		rbCol:   db.Collection("rbac_bindings"),
		psCol:   db.Collection("platform_subjects"),
		iuCol:   db.Collection("iam_users"),
		igCol:   db.Collection("iam_groups"),
		imCol:   db.Collection("iam_memberships"),
		isaCol:  db.Collection("iam_service_accounts"),
		imgCol:  db.Collection("platform_images"),
		secCol:  db.Collection("secrets"),
		aeCol:   db.Collection("audit_events"),
		kclCol:  db.Collection("k8s_clusters"),
		knsCol:  db.Collection("k8s_namespaces"),
		kdiCol:  db.Collection("k8s_deployment_inventory"),
		kdhCol:  db.Collection("k8s_deployment_history"),
	}
	if err := store.ensureCatalogIndexes(ctx); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("初始化服务索引失败: %w", err)
	}
	if err := store.ensureAlertingIndexes(ctx); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("初始化告警索引失败: %w", err)
	}
	if err := store.ensureMetricsIndexes(ctx); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("初始化指标索引失败: %w", err)
	}
	return store, nil
}

func (s *Store) ensureCatalogIndexes(ctx context.Context) error {
	_, err := s.prdCol.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "project_id", Value: 1}},
			Options: options.Index().SetName("uniq_product_project_id").SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "name", Value: 1}},
			Options: options.Index().SetName("uniq_product_name").SetUnique(true),
		},
	})
	if err != nil {
		return err
	}
	_, err = s.svcCol.Indexes().CreateOne(ctx, catalogServiceIndexModel())
	return err
}

func catalogServiceIndexModel() mongo.IndexModel {
	return mongo.IndexModel{
		Keys:    bson.D{{Key: "product_id", Value: 1}, {Key: "name", Value: 1}},
		Options: options.Index().SetName("uniq_product_service_name").SetUnique(true),
	}
}

func (s *Store) ensureAlertingIndexes(ctx context.Context) error {
	legacyRules, err := s.arCol.CountDocuments(ctx, bson.M{"spec": bson.M{"$exists": false}})
	if err != nil {
		return err
	}
	if legacyRules > 0 {
		return fmt.Errorf("检测到 %d 条旧版告警规则；新模型无法无损推导服务、日志路由和通知策略，请先备份并显式清理旧数据", legacyRules)
	}
	_, err = s.arCol.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "spec.scope.service_id", Value: 1}, {Key: "spec.name", Value: 1}}, Options: options.Index().SetName("uniq_service_rule_name").SetUnique(true)},
		{Keys: bson.D{{Key: "spec.scope.endpoint_id", Value: 1}, {Key: "state", Value: 1}}, Options: options.Index().SetName("runtime_enabled_rules")},
	})
	if err != nil {
		return err
	}
	_, err = s.areCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "rule_id", Value: 1}, {Key: "received_at", Value: -1}}, Options: options.Index().SetName("rule_event_history"),
	})
	if err != nil {
		return err
	}
	_, err = s.arpCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "service_id", Value: 1}, {Key: "name", Value: 1}}, Options: options.Index().SetName("uniq_service_policy_name").SetUnique(true),
	})
	return err
}

func (s *Store) ensureMetricsIndexes(ctx context.Context) error {
	_, err := s.msbCol.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "service_id", Value: 1}},
			Options: options.Index().
				SetName("uniq_active_metrics_binding").
				SetUnique(true).
				SetPartialFilterExpression(bson.M{"status": "active"}),
		},
		{
			Keys:    bson.D{{Key: "endpoint_id", Value: 1}, {Key: "status", Value: 1}},
			Options: options.Index().SetName("metrics_binding_endpoint_status"),
		},
	})
	if err != nil {
		return err
	}
	_, err = s.mrtCol.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "product_id", Value: 1}, {Key: "endpoint_id", Value: 1}, {Key: "cluster_id", Value: 1}, {Key: "namespace", Value: 1}, {Key: "k8s_service_name", Value: 1}, {Key: "port", Value: 1}, {Key: "metrics_path", Value: 1}},
			Options: options.Index().SetName("uniq_active_metrics_route_target").SetUnique(true).SetPartialFilterExpression(bson.M{"status": metricRouteActiveStatus}),
		},
		{Keys: bson.D{{Key: "service_id", Value: 1}, {Key: "updated_at", Value: -1}}, Options: options.Index().SetName("metrics_route_service_updated")},
		{Keys: bson.D{{Key: "cluster_id", Value: 1}, {Key: "product_id", Value: 1}, {Key: "endpoint_id", Value: 1}, {Key: "status", Value: 1}}, Options: options.Index().SetName("metrics_route_runtime_group")},
	})
	return err
}

const metricRouteActiveStatus = "active"

func (s *Store) Close(ctx context.Context) error {
	return s.client.Disconnect(ctx)
}

// ---------- sub-store accessors ----------

func (s *Store) Products() database.ProductStore                     { return &productStore{s.prdCol} }
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
func (s *Store) LogEndpoints() database.LogEndpointStore              { return &logEndpointStore{s.lgeCol} }
func (s *Store) LogSources() database.LogSourceStore                  { return &logSourceStore{s.lgsCol} }
func (s *Store) LogRoutes() database.LogRouteStore                    { return &logRouteStore{s.lgrCol} }
func (s *Store) LogTargets() database.LogTargetStore                  { return &logTargetStore{s.lgtCol} }
func (s *Store) LogCollectorConfigVersions() database.LogCollectorConfigVersionStore {
	return &logCollectorConfigVersionStore{s.lgcvCol}
}
func (s *Store) LogDeploymentManifestVersions() database.LogDeploymentManifestVersionStore {
	return &logDeploymentManifestVersionStore{s.lgdvCol}
}
func (s *Store) LogCollectorClusterConfigs() database.LogCollectorClusterConfigStore {
	return &logCollectorClusterConfigStore{s.lgccCol}
}
func (s *Store) ObservabilityRuntimes() database.ObservabilityRuntimeStore {
	return &observabilityRuntimeStore{s.ortCol}
}
func (s *Store) MetricsServiceBindings() database.MetricsServiceBindingStore {
	return &metricsServiceBindingStore{s.msbCol}
}
func (s *Store) MetricsRoutes() database.MetricsRouteStore { return &metricsRouteStore{s.mrtCol} }
func (s *Store) Alerting() database.AlertingStore {
	return &alertingStore{client: s.client, rules: s.arCol, updates: s.aruCol, instances: s.ariCol, events: s.areCol, policies: s.arpCol, audits: s.aeCol}
}
func (s *Store) RBACRoles() database.RBACRoleStore       { return &rbacRoleStore{s.rrCol} }
func (s *Store) RBACBindings() database.RBACBindingStore { return &rbacBindingStore{s.rbCol} }
func (s *Store) PlatformSubjects() database.PlatformSubjectStore {
	return &platformSubjectStore{s.psCol}
}
func (s *Store) IAMUsers() database.IAMUserStore             { return &iamUserStore{s.iuCol} }
func (s *Store) IAMGroups() database.IAMGroupStore           { return &iamGroupStore{s.igCol} }
func (s *Store) IAMMemberships() database.IAMMembershipStore { return &iamMembershipStore{s.imCol} }
func (s *Store) IAMServiceAccounts() database.IAMServiceAccountStore {
	return &iamServiceAccountStore{s.isaCol}
}
func (s *Store) PlatformImages() database.PlatformImageStore { return &platformImageStore{s.imgCol} }
func (s *Store) Secrets() database.SecretStore               { return &secretStore{s.secCol} }
func (s *Store) AuditEvents() database.AuditEventStore       { return &auditEventStore{s.aeCol} }
func (s *Store) K8sClusters() database.K8sClusterStore       { return &k8sClusterStore{s.kclCol} }
func (s *Store) K8sNamespaces() database.K8sNamespaceStore   { return &k8sNamespaceStore{s.knsCol} }
func (s *Store) K8sDeploymentInventory() database.K8sDeploymentInventoryStore {
	return &k8sDeploymentInventoryStore{s.kdiCol}
}
func (s *Store) K8sDeploymentHistory() database.K8sDeploymentHistoryStore {
	return &k8sDeploymentHistoryStore{s.kdhCol}
}

// ---------- helpers ----------

func objectID(id string) (interface{}, error) {
	return id, nil
}
