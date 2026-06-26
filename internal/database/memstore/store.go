package memstore

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"novaobs/internal/database"
)

// Store is a thread-safe in-memory implementation of database.Store.
// Useful for tests and local development without MongoDB.
type Store struct {
	mu    sync.RWMutex
	svcs  map[string]interface{}
	stgs  map[string]interface{}
	cgs   map[string]interface{}
	cis   map[string]interface{}
	ccvs  map[string]interface{}
	cpts  map[string]interface{}
	cgos  map[string]interface{}
	seps  map[string]interface{}
	sprs  map[string]interface{}
	spps  map[string]interface{}
	cacs  map[string]interface{}
	iis   map[string]interface{}
	onbs  map[string]interface{}
	lges  map[string]interface{}
	lgss  map[string]interface{}
	lgrs  map[string]interface{}
	lgcvs map[string]interface{}
	lgdvs map[string]interface{}
	lgps  map[string]interface{}
	lgccs map[string]interface{}
	ars   map[string]interface{}
	arus  map[string]interface{}
	aris  map[string]interface{}
	ares  map[string]interface{}
	arps  map[string]interface{}
	rrs   map[string]interface{}
	rbs   map[string]interface{}
	pss   map[string]interface{}
	iums  map[string]interface{}
	igns  map[string]interface{}
	imbs  map[string]interface{}
	isas  map[string]interface{}
	secs  map[string]interface{}
	aes   map[string]interface{}
	kcls  map[string]interface{}
	knss  map[string]interface{}
	kdis  map[string]interface{}
	kdhs  map[string]interface{}
}

func NewStore() *Store {
	return &Store{
		svcs:  map[string]interface{}{},
		stgs:  map[string]interface{}{},
		cgs:   map[string]interface{}{},
		cis:   map[string]interface{}{},
		ccvs:  map[string]interface{}{},
		cpts:  map[string]interface{}{},
		cgos:  map[string]interface{}{},
		seps:  map[string]interface{}{},
		sprs:  map[string]interface{}{},
		spps:  map[string]interface{}{},
		cacs:  map[string]interface{}{},
		iis:   map[string]interface{}{},
		onbs:  map[string]interface{}{},
		lges:  map[string]interface{}{},
		lgss:  map[string]interface{}{},
		lgrs:  map[string]interface{}{},
		lgcvs: map[string]interface{}{},
		lgdvs: map[string]interface{}{},
		lgps:  map[string]interface{}{},
		lgccs: map[string]interface{}{},
		ars:   map[string]interface{}{},
		arus:  map[string]interface{}{},
		aris:  map[string]interface{}{},
		ares:  map[string]interface{}{},
		arps:  map[string]interface{}{},
		rrs:   map[string]interface{}{},
		rbs:   map[string]interface{}{},
		pss:   map[string]interface{}{},
		iums:  map[string]interface{}{},
		igns:  map[string]interface{}{},
		imbs:  map[string]interface{}{},
		isas:  map[string]interface{}{},
		secs:  map[string]interface{}{},
		aes:   map[string]interface{}{},
		kcls:  map[string]interface{}{},
		knss:  map[string]interface{}{},
		kdis:  map[string]interface{}{},
		kdhs:  map[string]interface{}{},
	}
}

func (s *Store) Close(ctx context.Context) error { return nil }

// ---------- Sub-store accessors ----------

func (s *Store) Services() database.ServiceStore                     { return &svcStore{s} }
func (s *Store) ServiceTargets() database.ServiceTargetStore         { return &targetStore{s} }
func (s *Store) CollectorGroups() database.CollectorGroupStore       { return &cgStore{s} }
func (s *Store) CollectorInstances() database.CollectorInstanceStore { return &ciStore{s} }
func (s *Store) CollectorConfigVersions() database.CollectorConfigVersionStore {
	return &ccvStore{s}
}
func (s *Store) CollectorPlatformTemplates() database.CollectorPlatformTemplateStore {
	return &cptStore{s}
}
func (s *Store) CollectorGroupOverrides() database.CollectorGroupOverrideStore {
	return &cgoStore{s}
}
func (s *Store) ServiceEnrichmentPatches() database.ServiceEnrichmentPatchStore {
	return &sepStore{s}
}
func (s *Store) ServiceParserRules() database.ServiceParserRuleStore {
	return &sprStore{s}
}
func (s *Store) ServicePipelinePatches() database.ServicePipelinePatchStore {
	return &sppStore{s}
}
func (s *Store) IngestionIdentities() database.IngestionIdentityStore { return &iiStore{s} }
func (s *Store) Onboardings() database.OnboardingStore                { return &onbStore{s} }
func (s *Store) LogEndpoints() database.LogEndpointStore              { return &logEndpointStore{s} }
func (s *Store) LogSources() database.LogSourceStore                  { return &logSourceStore{s} }
func (s *Store) LogRoutes() database.LogRouteStore                    { return &logRouteStore{s} }
func (s *Store) LogCollectorConfigVersions() database.LogCollectorConfigVersionStore {
	return &logCollectorConfigVersionStore{s}
}
func (s *Store) LogDeploymentManifestVersions() database.LogDeploymentManifestVersionStore {
	return &logDeploymentManifestVersionStore{s}
}
func (s *Store) LogAgentPlans() database.LogAgentPlanStore { return &logAgentPlanStore{s} }
func (s *Store) LogCollectorClusterConfigs() database.LogCollectorClusterConfigStore {
	return &logCollectorClusterConfigStore{s}
}
func (s *Store) Alerting() database.AlertingStore                { return &alertingStore{s} }
func (s *Store) RBACRoles() database.RBACRoleStore               { return &rbacRoleStore{s} }
func (s *Store) RBACBindings() database.RBACBindingStore         { return &rbacBindingStore{s} }
func (s *Store) PlatformSubjects() database.PlatformSubjectStore { return &platformSubjectStore{s} }
func (s *Store) IAMUsers() database.IAMUserStore                 { return &iamUserStore{s} }
func (s *Store) IAMGroups() database.IAMGroupStore               { return &iamGroupStore{s} }
func (s *Store) IAMMemberships() database.IAMMembershipStore     { return &iamMembershipStore{s} }
func (s *Store) IAMServiceAccounts() database.IAMServiceAccountStore {
	return &iamServiceAccountStore{s}
}
func (s *Store) Secrets() database.SecretStore             { return &secretStore{s} }
func (s *Store) AuditEvents() database.AuditEventStore     { return &auditEventStore{s} }
func (s *Store) K8sClusters() database.K8sClusterStore     { return &k8sClusterStore{s} }
func (s *Store) K8sNamespaces() database.K8sNamespaceStore { return &k8sNamespaceStore{s} }
func (s *Store) K8sDeploymentInventory() database.K8sDeploymentInventoryStore {
	return &k8sDeploymentInventoryStore{s}
}
func (s *Store) K8sDeploymentHistory() database.K8sDeploymentHistoryStore {
	return &k8sDeploymentHistoryStore{s}
}

// ---------- Services ----------

type svcStore struct{ s *Store }

func (st *svcStore) Insert(ctx context.Context, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	// Extract ID via reflection-like approach: expect a struct with ID field
	id := extractID(v)
	if id == "" {
		id = newID()
	}
	st.s.svcs[id] = v
	return nil
}
func (st *svcStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.svcs, results)
}
func (st *svcStore) FindByID(ctx context.Context, id string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.svcs[id]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}
func (st *svcStore) Update(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.svcs[id] = v
	return nil
}
func (st *svcStore) Count(ctx context.Context) (int64, error) {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return int64(len(st.s.svcs)), nil
}

// ---------- Service Targets ----------

type targetStore struct{ s *Store }

func (st *targetStore) Insert(ctx context.Context, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	id := extractID(v)
	if id == "" {
		id = newID()
	}
	st.s.stgs[id] = v
	return nil
}

func (st *targetStore) FindByService(ctx context.Context, serviceID string, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for key, value := range st.s.stgs {
		if extractServiceID(value) == serviceID {
			filtered[key] = value
		}
	}
	return copyAll(filtered, results)
}

func (st *targetStore) FindByID(ctx context.Context, id string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.stgs[id]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}

func (st *targetStore) Update(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	if _, ok := st.s.stgs[id]; !ok {
		return errNotFound
	}
	st.s.stgs[id] = v
	return nil
}

// ---------- Collector Groups ----------

type cgStore struct{ s *Store }

func (st *cgStore) Insert(ctx context.Context, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	id := extractID(v)
	if id == "" {
		id = newID()
	}
	st.s.cgs[id] = v
	return nil
}
func (st *cgStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.cgs, results)
}
func (st *cgStore) FindByID(ctx context.Context, id string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.cgs[id]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}
func (st *cgStore) Update(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	if _, ok := st.s.cgs[id]; !ok {
		return errNotFound
	}
	st.s.cgs[id] = v
	return nil
}
func (st *cgStore) Count(ctx context.Context) (int64, error) {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return int64(len(st.s.cgs)), nil
}

// ---------- Collector Instances ----------

type ciStore struct{ s *Store }

func (st *ciStore) Upsert(ctx context.Context, instanceUID string, groupID string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	runtimeIdentity := extractStringField(v, "RuntimeIdentity")
	if runtimeIdentity != "" {
		for key, value := range st.s.cis {
			if key != instanceUID && extractStringField(value, "RuntimeIdentity") == runtimeIdentity {
				delete(st.s.cis, key)
			}
		}
	}
	st.s.cis[instanceUID] = v
	return nil
}
func (st *ciStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.cis, results)
}
func (st *ciStore) FindByGroup(ctx context.Context, groupID string, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for key, value := range st.s.cis {
		if extractCollectorGroupID(value) == groupID {
			filtered[key] = value
		}
	}
	return copyAll(filtered, results)
}
func (st *ciStore) FindByUID(ctx context.Context, instanceUID string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.cis[instanceUID]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}
func (st *ciStore) FindByRuntimeIdentity(ctx context.Context, runtimeIdentity string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	for _, value := range st.s.cis {
		if extractStringField(value, "RuntimeIdentity") == runtimeIdentity {
			return copyValue(value, result)
		}
	}
	return errNotFound
}
func (st *ciStore) Update(ctx context.Context, instanceUID string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	if _, ok := st.s.cis[instanceUID]; !ok {
		return errNotFound
	}
	st.s.cis[instanceUID] = v
	return nil
}
func (st *ciStore) Delete(ctx context.Context, instanceUID string) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	if _, ok := st.s.cis[instanceUID]; !ok {
		return errNotFound
	}
	delete(st.s.cis, instanceUID)
	return nil
}

// ---------- Collector Config Versions ----------

type ccvStore struct{ s *Store }

func (st *ccvStore) Insert(ctx context.Context, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	id := extractID(v)
	if id == "" {
		id = newID()
	}
	st.s.ccvs[id] = v
	return nil
}
func (st *ccvStore) FindByGroup(ctx context.Context, groupID string, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for key, value := range st.s.ccvs {
		if extractCollectorGroupID(value) == groupID {
			filtered[key] = value
		}
	}
	return copyAll(filtered, results)
}
func (st *ccvStore) UpdateStatusByGroupAndHash(ctx context.Context, groupID string, configHash string, updates map[string]interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	for key, value := range st.s.ccvs {
		if extractCollectorGroupID(value) == groupID && extractConfigHash(value) == configHash {
			updated, err := mergedValue(value, updates)
			if err != nil {
				return err
			}
			st.s.ccvs[key] = updated
			return nil
		}
	}
	return errNotFound
}

// ---------- Ingestion Identities ----------

type iiStore struct{ s *Store }

func (st *iiStore) Insert(ctx context.Context, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	id := extractID(v)
	if id == "" {
		id = newID()
	}
	st.s.iis[id] = v
	return nil
}
func (st *iiStore) Upsert(ctx context.Context, serviceID string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	for id, current := range st.s.iis {
		if extractServiceID(current) == serviceID {
			st.s.iis[id] = v
			return nil
		}
	}
	id := extractID(v)
	if id == "" {
		id = newID()
	}
	st.s.iis[id] = v
	return nil
}
func (st *iiStore) FindByService(ctx context.Context, serviceID string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	for _, v := range st.s.iis {
		if extractServiceID(v) == serviceID {
			return copyValue(v, result)
		}
	}
	return errNotFound
}

// ---------- Onboardings ----------

type onbStore struct{ s *Store }

func (st *onbStore) Upsert(ctx context.Context, serviceID string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.onbs[serviceID] = v
	return nil
}
func (st *onbStore) FindByService(ctx context.Context, serviceID string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.onbs[serviceID]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}
func (st *onbStore) FindByCollectorGroup(ctx context.Context, groupID string, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for key, value := range st.s.onbs {
		if extractCollectorGroupID(value) == groupID {
			filtered[key] = value
		}
	}
	return copyAll(filtered, results)
}

// ---------- Collector Config Model Stores ----------

type cptStore struct{ s *Store }

func (st *cptStore) Insert(ctx context.Context, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	id := extractID(v)
	if id == "" {
		id = newID()
	}
	st.s.cpts[id] = v
	return nil
}
func (st *cptStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.cpts, results)
}
func (st *cptStore) FindByID(ctx context.Context, id string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.cpts[id]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}
func (st *cptStore) Update(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	if _, ok := st.s.cpts[id]; !ok {
		return errNotFound
	}
	st.s.cpts[id] = v
	return nil
}

type cgoStore struct{ s *Store }

func (st *cgoStore) Upsert(ctx context.Context, groupID string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.cgos[groupID] = v
	return nil
}
func (st *cgoStore) FindByGroup(ctx context.Context, groupID string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.cgos[groupID]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}

type sepStore struct{ s *Store }
type sprStore struct{ s *Store }
type sppStore struct{ s *Store }

func upsertByService(ctx context.Context, store *Store, target map[string]interface{}, serviceID string, v interface{}) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	target[serviceID] = v
	return nil
}

func findByService(ctx context.Context, store *Store, target map[string]interface{}, serviceID string, result interface{}) error {
	store.mu.RLock()
	defer store.mu.RUnlock()
	v, ok := target[serviceID]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}

func findByCollectorGroup(ctx context.Context, store *Store, target map[string]interface{}, groupID string, results interface{}) error {
	store.mu.RLock()
	defer store.mu.RUnlock()
	filtered := map[string]interface{}{}
	for key, value := range target {
		if extractCollectorGroupID(value) == groupID {
			filtered[key] = value
		}
	}
	return copyAll(filtered, results)
}

func (st *sepStore) Upsert(ctx context.Context, serviceID string, v interface{}) error {
	return upsertByService(ctx, st.s, st.s.seps, serviceID, v)
}
func (st *sepStore) FindByService(ctx context.Context, serviceID string, result interface{}) error {
	return findByService(ctx, st.s, st.s.seps, serviceID, result)
}
func (st *sepStore) FindByCollectorGroup(ctx context.Context, groupID string, results interface{}) error {
	return findByCollectorGroup(ctx, st.s, st.s.seps, groupID, results)
}

func (st *sprStore) Upsert(ctx context.Context, serviceID string, v interface{}) error {
	return upsertByService(ctx, st.s, st.s.sprs, serviceID, v)
}
func (st *sprStore) FindByService(ctx context.Context, serviceID string, result interface{}) error {
	return findByService(ctx, st.s, st.s.sprs, serviceID, result)
}
func (st *sprStore) FindByCollectorGroup(ctx context.Context, groupID string, results interface{}) error {
	return findByCollectorGroup(ctx, st.s, st.s.sprs, groupID, results)
}

func (st *sppStore) Upsert(ctx context.Context, serviceID string, v interface{}) error {
	return upsertByService(ctx, st.s, st.s.spps, serviceID, v)
}
func (st *sppStore) FindByService(ctx context.Context, serviceID string, result interface{}) error {
	return findByService(ctx, st.s, st.s.spps, serviceID, result)
}
func (st *sppStore) FindByCollectorGroup(ctx context.Context, groupID string, results interface{}) error {
	return findByCollectorGroup(ctx, st.s, st.s.spps, groupID, results)
}

// ---------- Logs ----------

type logEndpointStore struct{ s *Store }

func (st *logEndpointStore) Insert(ctx context.Context, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	id := extractID(v)
	if id == "" {
		id = newID()
	}
	st.s.lges[id] = v
	return nil
}

func (st *logEndpointStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.lges, results)
}

func (st *logEndpointStore) FindByID(ctx context.Context, id string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.lges[id]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}

func (st *logEndpointStore) Update(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	if _, ok := st.s.lges[id]; !ok {
		return errNotFound
	}
	st.s.lges[id] = v
	return nil
}

type logSourceStore struct{ s *Store }

func (st *logSourceStore) Upsert(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	if strings.TrimSpace(id) == "" {
		id = extractID(v)
	}
	if id == "" {
		id = newID()
	}
	st.s.lgss[id] = v
	return nil
}

func (st *logSourceStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.lgss, results)
}

func (st *logSourceStore) FindByID(ctx context.Context, id string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.lgss[id]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}

type logRouteStore struct{ s *Store }

func (st *logRouteStore) Upsert(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	if strings.TrimSpace(id) == "" {
		id = extractID(v)
	}
	if id == "" {
		id = newID()
	}
	st.s.lgrs[id] = v
	return nil
}

func (st *logRouteStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.lgrs, results)
}

func (st *logRouteStore) FindByService(ctx context.Context, serviceID string, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for key, value := range st.s.lgrs {
		if extractServiceID(value) == serviceID {
			filtered[key] = value
		}
	}
	return copyAll(filtered, results)
}

func (st *logRouteStore) FindByAgentGroup(ctx context.Context, agentGroupID string, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for key, value := range st.s.lgrs {
		if extractStringField(value, "AgentGroupID") == agentGroupID {
			filtered[key] = value
		}
	}
	return copyAll(filtered, results)
}

func (st *logRouteStore) FindByID(ctx context.Context, id string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.lgrs[id]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}

func (st *logRouteStore) Update(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	if _, ok := st.s.lgrs[id]; !ok {
		return errNotFound
	}
	st.s.lgrs[id] = v
	return nil
}

type logCollectorConfigVersionStore struct{ s *Store }

func (st *logCollectorConfigVersionStore) Upsert(ctx context.Context, hash string, v interface{}) error {
	return upsertLogArtifact(ctx, st.s, st.s.lgcvs, hash, v)
}

func (st *logCollectorConfigVersionStore) FindByHash(ctx context.Context, hash string, result interface{}) error {
	return findLogArtifactByHash(ctx, st.s, st.s.lgcvs, hash, result)
}

type logDeploymentManifestVersionStore struct{ s *Store }

func (st *logDeploymentManifestVersionStore) Upsert(ctx context.Context, hash string, v interface{}) error {
	return upsertLogArtifact(ctx, st.s, st.s.lgdvs, hash, v)
}

func (st *logDeploymentManifestVersionStore) FindByHash(ctx context.Context, hash string, result interface{}) error {
	return findLogArtifactByHash(ctx, st.s, st.s.lgdvs, hash, result)
}

func upsertLogArtifact(ctx context.Context, store *Store, items map[string]interface{}, hash string, v interface{}) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := strings.TrimSpace(hash)
	if key == "" {
		key = extractConfigHash(v)
	}
	if key == "" {
		key = extractStringField(v, "DeploymentManifestHash")
	}
	if key == "" {
		key = extractID(v)
	}
	if key == "" {
		key = newID()
	}
	items[key] = v
	return nil
}

func findLogArtifactByHash(ctx context.Context, store *Store, items map[string]interface{}, hash string, result interface{}) error {
	store.mu.RLock()
	defer store.mu.RUnlock()
	value, ok := items[strings.TrimSpace(hash)]
	if !ok {
		return errNotFound
	}
	return copyValue(value, result)
}

type logAgentPlanStore struct{ s *Store }

func (st *logAgentPlanStore) Insert(ctx context.Context, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	id := extractID(v)
	if id == "" {
		id = newID()
	}
	st.s.lgps[id] = v
	return nil
}

func (st *logAgentPlanStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.lgps, results)
}

func (st *logAgentPlanStore) FindByRoute(ctx context.Context, routeID string, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for id, value := range st.s.lgps {
		if extractStringField(value, "RouteID") == routeID {
			filtered[id] = value
		}
	}
	return copyAll(filtered, results)
}

type logCollectorClusterConfigStore struct{ s *Store }

func (st *logCollectorClusterConfigStore) Upsert(ctx context.Context, clusterID string, agentNamespace string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.lgccs[clusterID+"\x00"+agentNamespace] = v
	return nil
}

func (st *logCollectorClusterConfigStore) FindByCluster(ctx context.Context, clusterID string, agentNamespace string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	value, ok := st.s.lgccs[clusterID+"\x00"+agentNamespace]
	if !ok {
		return errNotFound
	}
	return copyValue(value, result)
}

// ---------- Alerting ----------

type alertingStore struct{ s *Store }

func (st *alertingStore) SaveChange(_ context.Context, expectedCurrentUpdateID string, rule interface{}, update interface{}, auditEvent interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	ruleID := extractID(rule)
	if expectedCurrentUpdateID == "" {
		if _, exists := st.s.ars[ruleID]; exists {
			return database.ErrConflict
		}
		var candidate struct {
			Spec struct {
				Name  string `json:"name"`
				Scope struct {
					ServiceID string `json:"service_id"`
				} `json:"scope"`
			} `json:"spec"`
		}
		if err := copyValue(rule, &candidate); err != nil {
			return err
		}
		for _, existing := range st.s.ars {
			var index struct {
				Spec struct {
					Name  string `json:"name"`
					Scope struct {
						ServiceID string `json:"service_id"`
					} `json:"scope"`
				} `json:"spec"`
			}
			if err := copyValue(existing, &index); err != nil {
				return err
			}
			if index.Spec.Scope.ServiceID == candidate.Spec.Scope.ServiceID && index.Spec.Name == candidate.Spec.Name {
				return database.ErrConflict
			}
		}
	} else {
		stored, exists := st.s.ars[ruleID]
		if !exists {
			return database.ErrNotFound
		}
		var current struct {
			CurrentUpdateID string `json:"current_update_id"`
		}
		if err := copyValue(stored, &current); err != nil {
			return err
		}
		if current.CurrentUpdateID != expectedCurrentUpdateID {
			return database.ErrConflict
		}
	}
	st.s.ars[ruleID] = rule
	st.s.arus[extractID(update)] = update
	st.s.aes[extractID(auditEvent)] = auditEvent
	return nil
}

func (st *alertingStore) FindRules(_ context.Context, serviceID string, state string, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for id, raw := range st.s.ars {
		var index struct {
			State string `json:"state"`
			Spec  struct {
				Scope struct {
					ServiceID string `json:"service_id"`
				} `json:"scope"`
			} `json:"spec"`
		}
		if err := copyValue(raw, &index); err != nil {
			return err
		}
		if serviceID != "" && index.Spec.Scope.ServiceID != serviceID {
			continue
		}
		if state != "" && index.State != state {
			continue
		}
		filtered[id] = raw
	}
	return copyAll(filtered, results)
}

func (st *alertingStore) FindRuleByID(_ context.Context, id string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	raw, ok := st.s.ars[id]
	if !ok {
		return database.ErrNotFound
	}
	return copyValue(raw, result)
}

func (st *alertingStore) FindUpdate(_ context.Context, ruleID string, updateID string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	raw, ok := st.s.arus[updateID]
	if !ok {
		return database.ErrNotFound
	}
	var index struct {
		RuleID string `json:"rule_id"`
	}
	if err := copyValue(raw, &index); err != nil {
		return err
	}
	if index.RuleID != ruleID {
		return database.ErrNotFound
	}
	return copyValue(raw, result)
}

func (st *alertingStore) FindUpdates(_ context.Context, ruleID string, _ int, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for id, raw := range st.s.arus {
		var index struct {
			RuleID string `json:"rule_id"`
		}
		if err := copyValue(raw, &index); err != nil {
			return err
		}
		if index.RuleID == ruleID {
			filtered[id] = raw
		}
	}
	return copyAll(filtered, results)
}

func (st *alertingStore) FindRuntimeRules(_ context.Context, runtimeID string, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for id, raw := range st.s.ars {
		var index struct {
			Spec struct {
				Scope struct {
					EndpointID string `json:"endpoint_id"`
				} `json:"scope"`
			} `json:"spec"`
		}
		if err := copyValue(raw, &index); err != nil {
			return err
		}
		if index.Spec.Scope.EndpointID == runtimeID {
			filtered[id] = raw
		}
	}
	return copyAll(filtered, results)
}

func (st *alertingStore) MarkRuntimeRulesApplied(_ context.Context, endpointID string, appliedAt time.Time) (int64, error) {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	var applied int64
	for id, raw := range st.s.ars {
		var index struct {
			CurrentUpdateID string `json:"current_update_id"`
			Spec            struct {
				Scope struct {
					EndpointID string `json:"endpoint_id"`
				} `json:"scope"`
			} `json:"spec"`
		}
		if err := copyValue(raw, &index); err != nil {
			return applied, err
		}
		if index.Spec.Scope.EndpointID != endpointID {
			continue
		}
		rule, err := documentMap(raw)
		if err != nil {
			return applied, err
		}
		rule["apply_status"] = "applied"
		rule["applied_update_id"] = index.CurrentUpdateID
		rule["updated_at"] = appliedAt
		st.s.ars[id] = rule
		applied++
	}
	return applied, nil
}

func documentMap(value interface{}) (map[string]interface{}, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	document := map[string]interface{}{}
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, err
	}
	return document, nil
}

func (st *alertingStore) ApplyAlertEvent(_ context.Context, instance interface{}, event interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	fingerprint := extractStringField(instance, "Fingerprint")
	eventID := extractID(event)
	if fingerprint == "" || eventID == "" {
		return database.ErrConflict
	}
	if _, exists := st.s.ares[eventID]; !exists {
		eventDocument, err := documentMap(event)
		if err != nil {
			return err
		}
		if previous, ok := st.s.aris[fingerprint]; ok {
			var state struct {
				State string `json:"state"`
			}
			if err := copyValue(previous, &state); err != nil {
				return err
			}
			eventDocument["previous_state"] = state.State
		}
		st.s.ares[eventID] = eventDocument
	}
	st.s.aris[fingerprint] = instance
	return nil
}

func (st *alertingStore) FindAlertInstances(_ context.Context, ruleID string, serviceID string, state string, _ int, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for id, raw := range st.s.aris {
		var index struct {
			RuleID    string `json:"rule_id"`
			ServiceID string `json:"service_id"`
			State     string `json:"state"`
		}
		if err := copyValue(raw, &index); err != nil {
			return err
		}
		if (ruleID == "" || index.RuleID == ruleID) && (serviceID == "" || index.ServiceID == serviceID) && (state == "" || index.State == state) {
			filtered[id] = raw
		}
	}
	return copyAll(filtered, results)
}

func (st *alertingStore) FindAlertEvents(_ context.Context, ruleID string, fingerprint string, _ int, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for id, raw := range st.s.ares {
		var index struct {
			RuleID      string `json:"rule_id"`
			Fingerprint string `json:"fingerprint"`
		}
		if err := copyValue(raw, &index); err != nil {
			return err
		}
		if (ruleID == "" || index.RuleID == ruleID) && (fingerprint == "" || index.Fingerprint == fingerprint) {
			filtered[id] = raw
		}
	}
	return copyAll(filtered, results)
}

func (st *alertingStore) SaveNotificationPolicy(_ context.Context, expectedUpdatedAt time.Time, policy interface{}, auditEvent interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	policyID := extractID(policy)
	var candidate struct {
		Name      string `json:"name"`
		ServiceID string `json:"service_id"`
	}
	if err := copyValue(policy, &candidate); err != nil {
		return err
	}
	current, exists := st.s.arps[policyID]
	if expectedUpdatedAt.IsZero() {
		if exists {
			return database.ErrConflict
		}
	} else {
		if !exists {
			return database.ErrNotFound
		}
		var index struct {
			UpdatedAt time.Time `json:"updated_at"`
		}
		if err := copyValue(current, &index); err != nil {
			return err
		}
		if !index.UpdatedAt.Equal(expectedUpdatedAt) {
			return database.ErrConflict
		}
	}
	for id, raw := range st.s.arps {
		if id == policyID {
			continue
		}
		var index struct {
			Name      string `json:"name"`
			ServiceID string `json:"service_id"`
		}
		if err := copyValue(raw, &index); err != nil {
			return err
		}
		if index.Name == candidate.Name && index.ServiceID == candidate.ServiceID {
			return database.ErrConflict
		}
	}
	st.s.arps[policyID] = policy
	st.s.aes[extractID(auditEvent)] = auditEvent
	return nil
}

func (st *alertingStore) FindNotificationPolicyByID(_ context.Context, id string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	policy, exists := st.s.arps[id]
	if !exists {
		return database.ErrNotFound
	}
	return copyValue(policy, result)
}

func (st *alertingStore) FindNotificationPolicies(_ context.Context, serviceID string, enabledOnly bool, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for id, raw := range st.s.arps {
		var index struct {
			ServiceID string `json:"service_id"`
			Enabled   bool   `json:"enabled"`
		}
		if err := copyValue(raw, &index); err != nil {
			return err
		}
		if serviceID != "" && index.ServiceID != "" && index.ServiceID != serviceID {
			continue
		}
		if enabledOnly && !index.Enabled {
			continue
		}
		filtered[id] = raw
	}
	return copyAll(filtered, results)
}

// ---------- RBAC Stores ----------

type rbacRoleStore struct{ s *Store }

func (st *rbacRoleStore) Upsert(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.rrs[id] = v
	return nil
}

func (st *rbacRoleStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.rrs, results)
}

func (st *rbacRoleStore) FindByID(ctx context.Context, id string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.rrs[id]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}

func (st *rbacRoleStore) Delete(ctx context.Context, id string) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	delete(st.s.rrs, id)
	return nil
}

type rbacBindingStore struct{ s *Store }

func (st *rbacBindingStore) Upsert(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.rbs[id] = v
	return nil
}

func (st *rbacBindingStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.rbs, results)
}

func (st *rbacBindingStore) FindBySubject(ctx context.Context, subjectID string, subjectType string, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for key, value := range st.s.rbs {
		if extractStringField(value, "SubjectID") == subjectID && extractStringField(value, "SubjectType") == subjectType {
			filtered[key] = value
		}
	}
	return copyAll(filtered, results)
}

func (st *rbacBindingStore) Delete(ctx context.Context, id string) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	delete(st.s.rbs, id)
	return nil
}

type platformSubjectStore struct{ s *Store }

func (st *platformSubjectStore) Upsert(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.pss[id] = v
	return nil
}

func (st *platformSubjectStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.pss, results)
}

func (st *platformSubjectStore) Delete(ctx context.Context, id string) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	delete(st.s.pss, id)
	return nil
}

// ---------- IAM Stores ----------

type iamUserStore struct{ s *Store }

func (st *iamUserStore) Upsert(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.iums[id] = v
	return nil
}

func (st *iamUserStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.iums, results)
}

func (st *iamUserStore) FindByID(ctx context.Context, id string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.iums[id]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}

func (st *iamUserStore) Delete(ctx context.Context, id string) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	delete(st.s.iums, id)
	return nil
}

type iamGroupStore struct{ s *Store }

func (st *iamGroupStore) Upsert(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.igns[id] = v
	return nil
}

func (st *iamGroupStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.igns, results)
}

func (st *iamGroupStore) FindByID(ctx context.Context, id string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.igns[id]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}

func (st *iamGroupStore) Delete(ctx context.Context, id string) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	delete(st.s.igns, id)
	return nil
}

type iamMembershipStore struct{ s *Store }

func (st *iamMembershipStore) Upsert(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.imbs[id] = v
	return nil
}

func (st *iamMembershipStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.imbs, results)
}

func (st *iamMembershipStore) FindByGroup(ctx context.Context, groupID string, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for key, value := range st.s.imbs {
		if extractStringField(value, "GroupID") == groupID {
			filtered[key] = value
		}
	}
	return copyAll(filtered, results)
}

func (st *iamMembershipStore) FindBySubject(ctx context.Context, subjectID string, subjectType string, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for key, value := range st.s.imbs {
		if extractStringField(value, "SubjectID") == subjectID && extractStringField(value, "SubjectType") == subjectType {
			filtered[key] = value
		}
	}
	return copyAll(filtered, results)
}

func (st *iamMembershipStore) Delete(ctx context.Context, id string) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	delete(st.s.imbs, id)
	return nil
}

type iamServiceAccountStore struct{ s *Store }

func (st *iamServiceAccountStore) Upsert(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.isas[id] = v
	return nil
}

func (st *iamServiceAccountStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.isas, results)
}

func (st *iamServiceAccountStore) FindByID(ctx context.Context, id string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.isas[id]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}

func (st *iamServiceAccountStore) Delete(ctx context.Context, id string) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	delete(st.s.isas, id)
	return nil
}

// ---------- Secret Store ----------

type secretStore struct{ s *Store }

func (st *secretStore) Upsert(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.secs[id] = v
	return nil
}

func (st *secretStore) FindByID(ctx context.Context, id string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	v, ok := st.s.secs[id]
	if !ok {
		return errNotFound
	}
	return copyValue(v, result)
}

func (st *secretStore) FindByTypeAndScope(ctx context.Context, typ string, scope interface{}, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	for _, value := range st.s.secs {
		if secretMatchesTypeAndScope(value, typ, scope) {
			return copyValue(value, result)
		}
	}
	return errNotFound
}

func (st *secretStore) FindByType(ctx context.Context, typ string, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for key, value := range st.s.secs {
		if extractStringField(value, "Type") == typ {
			filtered[key] = value
		}
	}
	return copyAll(filtered, results)
}

func secretMatchesTypeAndScope(value interface{}, typ string, scope interface{}) bool {
	var doc struct {
		Type  string         `json:"type"`
		Scope map[string]any `json:"scope"`
	}
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return false
	}
	if doc.Type != typ {
		return false
	}
	var wanted map[string]any
	data, err = json.Marshal(scope)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(data, &wanted); err != nil {
		return false
	}
	return stringValue(doc.Scope["cluster_id"]) == stringValue(wanted["cluster_id"]) &&
		stringValue(doc.Scope["namespace"]) == stringValue(wanted["namespace"]) &&
		stringValue(doc.Scope["service_id"]) == stringValue(wanted["service_id"])
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

// ---------- Audit Events ----------

type auditEventStore struct{ s *Store }

func (st *auditEventStore) Insert(ctx context.Context, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	id := extractID(v)
	if id == "" {
		id = newID()
	}
	st.s.aes[id] = v
	return nil
}

func (st *auditEventStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.aes, results)
}

// ---------- K8s Ops Stores ----------

type k8sClusterStore struct{ s *Store }

func (st *k8sClusterStore) Upsert(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.kcls[id] = v
	return nil
}

func (st *k8sClusterStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.kcls, results)
}

func (st *k8sClusterStore) Delete(ctx context.Context, id string) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	delete(st.s.kcls, id)
	return nil
}

type k8sNamespaceStore struct{ s *Store }

func (st *k8sNamespaceStore) Upsert(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.knss[id] = v
	return nil
}

func (st *k8sNamespaceStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.knss, results)
}

func (st *k8sNamespaceStore) FindByCluster(ctx context.Context, clusterID string, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	filtered := map[string]interface{}{}
	for key, value := range st.s.knss {
		if extractStringField(value, "ClusterID") == clusterID {
			filtered[key] = value
		}
	}
	return copyAll(filtered, results)
}

type k8sDeploymentInventoryStore struct{ s *Store }

func (st *k8sDeploymentInventoryStore) Upsert(ctx context.Context, id string, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.kdis[id] = v
	return nil
}

func (st *k8sDeploymentInventoryStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.kdis, results)
}

func (st *k8sDeploymentInventoryStore) FindByIdentity(ctx context.Context, clusterID string, namespace string, apiVersion string, kind string, name string, result interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	for _, value := range st.s.kdis {
		if extractStringField(value, "ClusterID") == clusterID &&
			extractStringField(value, "Namespace") == namespace &&
			extractStringField(value, "APIVersion") == apiVersion &&
			strings.EqualFold(extractStringField(value, "Kind"), kind) &&
			extractStringField(value, "Name") == name {
			return copyValue(value, result)
		}
	}
	return errNotFound
}

func (st *k8sDeploymentInventoryStore) Delete(ctx context.Context, id string) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	if _, ok := st.s.kdis[id]; !ok {
		return errNotFound
	}
	delete(st.s.kdis, id)
	return nil
}

type k8sDeploymentHistoryStore struct{ s *Store }

func (st *k8sDeploymentHistoryStore) Insert(ctx context.Context, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	id := extractID(v)
	if id == "" {
		id = newID()
	}
	st.s.kdhs[id] = v
	return nil
}

func (st *k8sDeploymentHistoryStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.kdhs, results)
}
