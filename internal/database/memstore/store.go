package memstore

import (
	"context"
	"sync"

	"novaobs/internal/database"
)

// Store is a thread-safe in-memory implementation of database.Store.
// Useful for tests and local development without MongoDB.
type Store struct {
	mu   sync.RWMutex
	svcs map[string]interface{}
	stgs map[string]interface{}
	cgs  map[string]interface{}
	cis  map[string]interface{}
	ccvs map[string]interface{}
	cpts map[string]interface{}
	cgos map[string]interface{}
	seps map[string]interface{}
	sprs map[string]interface{}
	spps map[string]interface{}
	cacs map[string]interface{}
	iis  map[string]interface{}
	onbs map[string]interface{}
	ars  map[string]interface{}
}

func NewStore() *Store {
	return &Store{
		svcs: map[string]interface{}{},
		stgs: map[string]interface{}{},
		cgs:  map[string]interface{}{},
		cis:  map[string]interface{}{},
		ccvs: map[string]interface{}{},
		cpts: map[string]interface{}{},
		cgos: map[string]interface{}{},
		seps: map[string]interface{}{},
		sprs: map[string]interface{}{},
		spps: map[string]interface{}{},
		cacs: map[string]interface{}{},
		iis:  map[string]interface{}{},
		onbs: map[string]interface{}{},
		ars:  map[string]interface{}{},
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
func (s *Store) AlertRules() database.AlertRuleStore                  { return &arStore{s} }

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

// ---------- Alert Rules ----------

type arStore struct{ s *Store }

func (st *arStore) Insert(ctx context.Context, v interface{}) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	id := extractID(v)
	if id == "" {
		id = newID()
	}
	st.s.ars[id] = v
	return nil
}
func (st *arStore) FindAll(ctx context.Context, results interface{}) error {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return copyAll(st.s.ars, results)
}
func (st *arStore) Count(ctx context.Context) (int64, error) {
	st.s.mu.RLock()
	defer st.s.mu.RUnlock()
	return int64(len(st.s.ars)), nil
}
