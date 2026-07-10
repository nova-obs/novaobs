package mongo

import (
	"context"
	"reflect"
	"strings"
	"time"

	"novaapm/internal/database"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type productStore struct{ col *mongo.Collection }

func (s *productStore) Insert(ctx context.Context, product interface{}) error {
	_, err := s.col.InsertOne(ctx, product)
	if mongo.IsDuplicateKeyError(err) {
		return database.ErrConflict
	}
	return err
}
func (s *productStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"name": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}
func (s *productStore) FindByID(ctx context.Context, id string, result interface{}) error {
	oid, _ := objectID(id)
	return s.col.FindOne(ctx, bson.M{"_id": oid}).Decode(result)
}
func (s *productStore) Update(ctx context.Context, id string, product interface{}) error {
	oid, _ := objectID(id)
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": oid}, product)
	if mongo.IsDuplicateKeyError(err) {
		return database.ErrConflict
	}
	return err
}

// ---------- ServiceStore ----------
type svcStore struct{ col *mongo.Collection }

func (s *svcStore) Insert(ctx context.Context, svc interface{}) error {
	_, err := s.col.InsertOne(ctx, svc)
	if mongo.IsDuplicateKeyError(err) {
		return database.ErrConflict
	}
	return err
}
func (s *svcStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}
func (s *svcStore) FindByID(ctx context.Context, id string, result interface{}) error {
	oid, _ := objectID(id)
	return s.col.FindOne(ctx, bson.M{"_id": oid}).Decode(result)
}
func (s *svcStore) Update(ctx context.Context, id string, svc interface{}) error {
	oid, _ := objectID(id)
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": oid}, svc)
	return err
}
func (s *svcStore) Count(ctx context.Context) (int64, error) {
	return s.col.CountDocuments(ctx, bson.M{})
}

// ---------- ServiceTargetStore ----------
type targetStore struct{ col *mongo.Collection }

func (s *targetStore) Insert(ctx context.Context, target interface{}) error {
	_, err := s.col.InsertOne(ctx, target)
	return err
}
func (s *targetStore) FindByService(ctx context.Context, serviceID string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"service_id": serviceID}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}
func (s *targetStore) FindByID(ctx context.Context, id string, result interface{}) error {
	oid, _ := objectID(id)
	return s.col.FindOne(ctx, bson.M{"_id": oid}).Decode(result)
}
func (s *targetStore) Update(ctx context.Context, id string, target interface{}) error {
	oid, _ := objectID(id)
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": oid}, target)
	return err
}

// ---------- CollectorGroupStore ----------
type cgStore struct{ col *mongo.Collection }

func (s *cgStore) Insert(ctx context.Context, group interface{}) error {
	_, err := s.col.InsertOne(ctx, group)
	return err
}
func (s *cgStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}
func (s *cgStore) FindByID(ctx context.Context, id string, result interface{}) error {
	oid, _ := objectID(id)
	return s.col.FindOne(ctx, bson.M{"_id": oid}).Decode(result)
}
func (s *cgStore) Update(ctx context.Context, id string, group interface{}) error {
	oid, _ := objectID(id)
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": oid}, group)
	return err
}
func (s *cgStore) Count(ctx context.Context) (int64, error) {
	return s.col.CountDocuments(ctx, bson.M{})
}

// ---------- CollectorInstanceStore ----------
type ciStore struct{ col *mongo.Collection }

func (s *ciStore) Upsert(ctx context.Context, instanceUID string, groupID string, instance interface{}) error {
	filter, setDoc, insertID, err := collectorInstanceUpsertDocument(instanceUID, groupID, instance)
	if err != nil {
		return err
	}
	_, err = s.col.UpdateOne(ctx, filter, bson.M{
		"$set":         setDoc,
		"$setOnInsert": bson.M{"_id": insertID},
	}, options.Update().SetUpsert(true))
	return err
}

func collectorInstanceUpsertDocument(instanceUID string, groupID string, instance interface{}) (bson.M, bson.M, string, error) {
	setDoc, err := toBSONMap(instance)
	if err != nil {
		return nil, nil, "", err
	}
	filter := bson.M{"instance_uid": instanceUID}
	if runtimeIdentity, _ := setDoc["runtime_identity"].(string); strings.TrimSpace(runtimeIdentity) != "" {
		filter = bson.M{"$or": []bson.M{
			{"runtime_identity": runtimeIdentity},
			{"instance_uid": instanceUID},
		}}
	}
	insertID := normalizeDocumentID(setDoc, instanceUID)
	delete(setDoc, "_id")
	setDoc["instance_uid"] = instanceUID
	if opampUID, _ := setDoc["opamp_instance_uid"].(string); strings.TrimSpace(opampUID) == "" {
		setDoc["opamp_instance_uid"] = instanceUID
	}
	setDoc["collector_group_id"] = groupID
	return filter, setDoc, insertID, nil
}
func (s *ciStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	var docs []bson.M
	if err := cursor.All(ctx, &docs); err != nil {
		return err
	}
	return decodeBSONDocuments(docs, results)
}
func (s *ciStore) FindByGroup(ctx context.Context, groupID string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"collector_group_id": groupID}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	var docs []bson.M
	if err := cursor.All(ctx, &docs); err != nil {
		return err
	}
	return decodeBSONDocuments(docs, results)
}
func (s *ciStore) FindByUID(ctx context.Context, instanceUID string, result interface{}) error {
	var doc bson.M
	if err := s.col.FindOne(ctx, bson.M{"instance_uid": instanceUID}).Decode(&doc); err != nil {
		return err
	}
	normalizeDocumentID(doc, instanceUID)
	return decodeBSONDocument(doc, result)
}
func (s *ciStore) FindByRuntimeIdentity(ctx context.Context, runtimeIdentity string, result interface{}) error {
	var doc bson.M
	if err := s.col.FindOne(ctx, bson.M{"runtime_identity": runtimeIdentity}).Decode(&doc); err != nil {
		return err
	}
	fallback, _ := doc["instance_uid"].(string)
	normalizeDocumentID(doc, fallback)
	return decodeBSONDocument(doc, result)
}
func (s *ciStore) Update(ctx context.Context, instanceUID string, instance interface{}) error {
	setDoc, err := toBSONMap(instance)
	if err != nil {
		return err
	}
	delete(setDoc, "_id")
	setDoc["instance_uid"] = instanceUID
	result, err := s.col.UpdateOne(ctx, bson.M{"instance_uid": instanceUID}, bson.M{"$set": setDoc})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}
func (s *ciStore) Delete(ctx context.Context, instanceUID string) error {
	result, err := s.col.DeleteOne(ctx, bson.M{"instance_uid": instanceUID})
	if err != nil {
		return err
	}
	if result.DeletedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}

func toBSONMap(value interface{}) (bson.M, error) {
	if doc, ok := value.(bson.M); ok {
		return doc, nil
	}
	raw, err := bson.Marshal(value)
	if err != nil {
		return nil, err
	}
	var doc bson.M
	if err := bson.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func normalizeDocumentID(doc bson.M, fallback string) string {
	var id string
	switch value := doc["_id"].(type) {
	case string:
		id = value
	case primitive.ObjectID:
		id = value.Hex()
	}
	if id == "" {
		id = fallback
	}
	doc["_id"] = id
	return id
}

func scopedUpsertDocuments(value interface{}, fallbackID string) (bson.M, string, error) {
	setDoc, err := toBSONMap(value)
	if err != nil {
		return nil, "", err
	}
	insertID := normalizeDocumentID(setDoc, fallbackID)
	delete(setDoc, "_id")
	return setDoc, insertID, nil
}

func decodeBSONDocuments(docs []bson.M, results interface{}) error {
	target := reflect.ValueOf(results)
	if target.Kind() != reflect.Pointer || target.Elem().Kind() != reflect.Slice {
		return bson.Unmarshal([]byte{}, results)
	}
	slice := target.Elem()
	elemType := slice.Type().Elem()
	out := reflect.MakeSlice(slice.Type(), 0, len(docs))
	for _, doc := range docs {
		fallback, _ := doc["instance_uid"].(string)
		normalizeDocumentID(doc, fallback)
		elem := reflect.New(elemType)
		if err := decodeBSONDocument(doc, elem.Interface()); err != nil {
			return err
		}
		out = reflect.Append(out, elem.Elem())
	}
	slice.Set(out)
	return nil
}

func decodeBSONDocument(doc bson.M, result interface{}) error {
	raw, err := bson.Marshal(doc)
	if err != nil {
		return err
	}
	return bson.Unmarshal(raw, result)
}

// ---------- CollectorConfigVersionStore ----------
type ccvStore struct{ col *mongo.Collection }

func (s *ccvStore) Insert(ctx context.Context, version interface{}) error {
	_, err := s.col.InsertOne(ctx, version)
	return err
}
func (s *ccvStore) FindByGroup(ctx context.Context, groupID string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"collector_group_id": groupID}, options.Find().SetSort(bson.M{"version": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}
func (s *ccvStore) UpdateStatusByGroupAndHash(ctx context.Context, groupID string, configHash string, updates map[string]interface{}) error {
	result, err := s.col.UpdateOne(ctx, bson.M{"collector_group_id": groupID, "config_hash": configHash}, bson.M{"$set": updates})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

// ---------- IngestionIdentityStore ----------
type iiStore struct{ col *mongo.Collection }

func (s *iiStore) Insert(ctx context.Context, identity interface{}) error {
	_, err := s.col.InsertOne(ctx, identity)
	return err
}
func (s *iiStore) Upsert(ctx context.Context, serviceID string, identity interface{}) error {
	oid, _ := objectID(serviceID)
	filter := bson.M{"service_id": oid}
	opts := options.Update().SetUpsert(true)
	_, err := s.col.UpdateOne(ctx, filter, bson.M{"$set": identity}, opts)
	return err
}
func (s *iiStore) FindByService(ctx context.Context, serviceID string, result interface{}) error {
	oid, _ := objectID(serviceID)
	return s.col.FindOne(ctx, bson.M{"service_id": oid}).Decode(result)
}

// ---------- OnboardingStore ----------
type onbStore struct{ col *mongo.Collection }

func (s *onbStore) Upsert(ctx context.Context, serviceID string, onboarding interface{}) error {
	oid, _ := objectID(serviceID)
	filter := bson.M{"service_id": oid}
	update := bson.M{"$set": onboarding}
	opts := options.Update().SetUpsert(true)
	_, err := s.col.UpdateOne(ctx, filter, update, opts)
	return err
}
func (s *onbStore) FindByService(ctx context.Context, serviceID string, result interface{}) error {
	oid, _ := objectID(serviceID)
	return s.col.FindOne(ctx, bson.M{"service_id": oid}).Decode(result)
}
func (s *onbStore) FindByCollectorGroup(ctx context.Context, groupID string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"collector_group_id": groupID}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

// ---------- Collector Config Model Stores ----------
type cptStore struct{ col *mongo.Collection }

func (s *cptStore) Insert(ctx context.Context, template interface{}) error {
	_, err := s.col.InsertOne(ctx, template)
	return err
}
func (s *cptStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"updated_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}
func (s *cptStore) FindByID(ctx context.Context, id string, result interface{}) error {
	oid, _ := objectID(id)
	return s.col.FindOne(ctx, bson.M{"_id": oid}).Decode(result)
}
func (s *cptStore) Update(ctx context.Context, id string, template interface{}) error {
	oid, _ := objectID(id)
	result, err := s.col.ReplaceOne(ctx, bson.M{"_id": oid}, template)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

type cgoStore struct{ col *mongo.Collection }

func (s *cgoStore) Upsert(ctx context.Context, groupID string, override interface{}) error {
	opts := options.Update().SetUpsert(true)
	_, err := s.col.UpdateOne(ctx, bson.M{"collector_group_id": groupID}, bson.M{"$set": override}, opts)
	return err
}
func (s *cgoStore) FindByGroup(ctx context.Context, groupID string, result interface{}) error {
	return s.col.FindOne(ctx, bson.M{"collector_group_id": groupID}).Decode(result)
}

type serviceScopedStore struct{ col *mongo.Collection }

func (s *serviceScopedStore) Upsert(ctx context.Context, serviceID string, value interface{}) error {
	opts := options.Update().SetUpsert(true)
	setDoc, insertID, err := scopedUpsertDocuments(value, serviceID)
	if err != nil {
		return err
	}
	setDoc["service_id"] = serviceID
	_, err = s.col.UpdateOne(ctx, bson.M{"service_id": serviceID}, bson.M{
		"$set":         setDoc,
		"$setOnInsert": bson.M{"_id": insertID},
	}, opts)
	return err
}
func (s *serviceScopedStore) FindByService(ctx context.Context, serviceID string, result interface{}) error {
	return s.col.FindOne(ctx, bson.M{"service_id": serviceID}).Decode(result)
}
func (s *serviceScopedStore) FindByCollectorGroup(ctx context.Context, groupID string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"collector_group_id": groupID}, options.Find().SetSort(bson.M{"service_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

// ---------- Logs ----------

type logEndpointStore struct{ col *mongo.Collection }

func (s *logEndpointStore) Insert(ctx context.Context, endpoint interface{}) error {
	_, err := s.col.InsertOne(ctx, endpoint)
	return err
}

func (s *logEndpointStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"updated_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *logEndpointStore) FindByID(ctx context.Context, id string, result interface{}) error {
	oid, _ := objectID(id)
	return s.col.FindOne(ctx, bson.M{"_id": oid}).Decode(result)
}

func (s *logEndpointStore) Update(ctx context.Context, id string, endpoint interface{}) error {
	oid, _ := objectID(id)
	setDoc, err := toBSONMap(endpoint)
	if err != nil {
		return err
	}
	delete(setDoc, "_id")
	result, err := s.col.UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": setDoc})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

type logSourceStore struct{ col *mongo.Collection }

func (s *logSourceStore) Upsert(ctx context.Context, id string, source interface{}) error {
	setDoc, insertID, err := scopedUpsertDocuments(source, id)
	if err != nil {
		return err
	}
	_, err = s.col.UpdateOne(ctx, bson.M{"_id": insertID}, bson.M{
		"$set":         setDoc,
		"$setOnInsert": bson.M{"_id": insertID},
	}, options.Update().SetUpsert(true))
	return err
}

func (s *logSourceStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"updated_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *logSourceStore) FindByID(ctx context.Context, id string, result interface{}) error {
	oid, _ := objectID(id)
	return s.col.FindOne(ctx, bson.M{"_id": oid}).Decode(result)
}

type logRouteStore struct{ col *mongo.Collection }

func (s *logRouteStore) Upsert(ctx context.Context, id string, route interface{}) error {
	setDoc, insertID, err := scopedUpsertDocuments(route, id)
	if err != nil {
		return err
	}
	_, err = s.col.UpdateOne(ctx, bson.M{"_id": insertID}, bson.M{
		"$set":         setDoc,
		"$setOnInsert": bson.M{"_id": insertID},
	}, options.Update().SetUpsert(true))
	return err
}

func (s *logRouteStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"updated_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *logRouteStore) FindByService(ctx context.Context, serviceID string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"service_id": serviceID}, options.Find().SetSort(bson.M{"updated_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *logRouteStore) FindByAgentGroup(ctx context.Context, agentGroupID string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"agent_group_id": agentGroupID}, options.Find().SetSort(bson.M{"updated_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *logRouteStore) FindByID(ctx context.Context, id string, result interface{}) error {
	oid, _ := objectID(id)
	return s.col.FindOne(ctx, bson.M{"_id": oid}).Decode(result)
}

func (s *logRouteStore) Update(ctx context.Context, id string, route interface{}) error {
	oid, _ := objectID(id)
	result, err := s.col.ReplaceOne(ctx, bson.M{"_id": oid}, route)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

func (s *logRouteStore) Delete(ctx context.Context, id string) error {
	oid, _ := objectID(id)
	result, err := s.col.DeleteOne(ctx, bson.M{"_id": oid})
	if err != nil {
		return err
	}
	if result.DeletedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

type logTargetStore struct{ col *mongo.Collection }

func (s *logTargetStore) Insert(ctx context.Context, target interface{}) error {
	_, err := s.col.InsertOne(ctx, target)
	return err
}

func (s *logTargetStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"updated_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *logTargetStore) FindByService(ctx context.Context, serviceID string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"service_id": serviceID}, options.Find().SetSort(bson.M{"updated_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *logTargetStore) FindByID(ctx context.Context, id string, result interface{}) error {
	oid, _ := objectID(id)
	return s.col.FindOne(ctx, bson.M{"_id": oid}).Decode(result)
}

func (s *logTargetStore) Update(ctx context.Context, id string, target interface{}) error {
	oid, _ := objectID(id)
	result, err := s.col.ReplaceOne(ctx, bson.M{"_id": oid}, target)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

type logCollectorConfigVersionStore struct{ col *mongo.Collection }

func (s *logCollectorConfigVersionStore) Upsert(ctx context.Context, hash string, version interface{}) error {
	return upsertLogArtifactByHash(ctx, s.col, hash, "collector_config_hash", version)
}

func (s *logCollectorConfigVersionStore) FindByHash(ctx context.Context, hash string, result interface{}) error {
	return s.col.FindOne(ctx, bson.M{"collector_config_hash": hash}).Decode(result)
}

type logDeploymentManifestVersionStore struct{ col *mongo.Collection }

func (s *logDeploymentManifestVersionStore) Upsert(ctx context.Context, hash string, version interface{}) error {
	return upsertLogArtifactByHash(ctx, s.col, hash, "deployment_manifest_hash", version)
}

func (s *logDeploymentManifestVersionStore) FindByHash(ctx context.Context, hash string, result interface{}) error {
	return s.col.FindOne(ctx, bson.M{"deployment_manifest_hash": hash}).Decode(result)
}

func upsertLogArtifactByHash(ctx context.Context, col *mongo.Collection, hash string, hashField string, artifact interface{}) error {
	setDoc, err := toBSONMap(artifact)
	if err != nil {
		return err
	}
	insertID := hash
	if insertID == "" {
		if raw, ok := setDoc[hashField].(string); ok {
			insertID = raw
		}
	}
	delete(setDoc, "_id")
	_, err = col.UpdateOne(ctx, bson.M{hashField: insertID}, bson.M{
		"$set":         setDoc,
		"$setOnInsert": bson.M{"_id": insertID},
	}, options.Update().SetUpsert(true))
	return err
}

// ---------- LogCollectorClusterConfigStore ----------
type logCollectorClusterConfigStore struct{ col *mongo.Collection }

func (s *logCollectorClusterConfigStore) Upsert(ctx context.Context, clusterID string, agentNamespace string, config interface{}) error {
	id := clusterID + "\x00" + agentNamespace
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": id}, config, options.Replace().SetUpsert(true))
	return err
}

func (s *logCollectorClusterConfigStore) FindByCluster(ctx context.Context, clusterID string, agentNamespace string, result interface{}) error {
	id := clusterID + "\x00" + agentNamespace
	return s.col.FindOne(ctx, bson.M{"_id": id}).Decode(result)
}

// ---------- ObservabilityRuntimeStore ----------
type observabilityRuntimeStore struct{ col *mongo.Collection }

func (s *observabilityRuntimeStore) Upsert(ctx context.Context, id string, runtime interface{}) error {
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": id}, runtime, options.Replace().SetUpsert(true))
	return err
}

func (s *observabilityRuntimeStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"updated_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *observabilityRuntimeStore) FindByID(ctx context.Context, id string, result interface{}) error {
	return s.col.FindOne(ctx, bson.M{"_id": id}).Decode(result)
}

func (s *observabilityRuntimeStore) FindByCluster(ctx context.Context, clusterID string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"cluster_id": clusterID}, options.Find().SetSort(bson.M{"kind": 1, "updated_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

// ---------- Metrics 服务绑定 ----------
type metricsServiceBindingStore struct{ col *mongo.Collection }

func (s *metricsServiceBindingStore) Insert(ctx context.Context, binding interface{}) error {
	_, err := s.col.InsertOne(ctx, binding)
	if mongo.IsDuplicateKeyError(err) {
		return database.ErrConflict
	}
	return err
}

func (s *metricsServiceBindingStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"updated_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *metricsServiceBindingStore) FindByService(ctx context.Context, serviceID string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"service_id": serviceID}, options.Find().SetSort(bson.M{"updated_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *metricsServiceBindingStore) FindByID(ctx context.Context, id string, result interface{}) error {
	oid, _ := objectID(id)
	return s.col.FindOne(ctx, bson.M{"_id": oid}).Decode(result)
}

func (s *metricsServiceBindingStore) Update(ctx context.Context, id string, binding interface{}) error {
	oid, _ := objectID(id)
	setDoc, err := toBSONMap(binding)
	if err != nil {
		return err
	}
	delete(setDoc, "_id")
	result, err := s.col.UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": setDoc})
	if mongo.IsDuplicateKeyError(err) {
		return database.ErrConflict
	}
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

// ---------- Alerting Repository ----------

type alertingStore struct {
	client    *mongo.Client
	rules     *mongo.Collection
	updates   *mongo.Collection
	instances *mongo.Collection
	events    *mongo.Collection
	policies  *mongo.Collection
	audits    *mongo.Collection
}

func (s *alertingStore) SaveChange(ctx context.Context, expectedCurrentUpdateID string, rule interface{}, update interface{}, auditEvent interface{}) error {
	session, err := s.client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		if expectedCurrentUpdateID == "" {
			if _, err := s.rules.InsertOne(tx, rule); err != nil {
				if mongo.IsDuplicateKeyError(err) {
					return nil, database.ErrConflict
				}
				return nil, err
			}
		} else {
			ruleID := reflect.Indirect(reflect.ValueOf(rule)).FieldByName("ID").String()
			result, err := s.rules.ReplaceOne(tx, bson.M{
				"_id":               ruleID,
				"current_update_id": expectedCurrentUpdateID,
			}, rule)
			if err != nil {
				if mongo.IsDuplicateKeyError(err) {
					return nil, database.ErrConflict
				}
				return nil, err
			}
			if result.MatchedCount == 0 {
				return nil, database.ErrConflict
			}
		}
		if _, err := s.updates.InsertOne(tx, update); err != nil {
			return nil, err
		}
		if _, err := s.audits.InsertOne(tx, auditEvent); err != nil {
			return nil, err
		}
		return nil, nil
	})
	return err
}

func (s *alertingStore) FindRules(ctx context.Context, serviceID string, state string, signalType string, results interface{}) error {
	query := bson.M{"spec.scope.service_id": bson.M{"$exists": true}}
	if serviceID != "" {
		query["spec.scope.service_id"] = serviceID
	}
	if state != "" {
		query["state"] = state
	}
	query = withAlertSignalFilter(query, signalType)
	cursor, err := s.rules.Find(ctx, query, options.Find().SetSort(bson.D{{Key: "updated_at", Value: -1}}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *alertingStore) FindRuleByID(ctx context.Context, id string, result interface{}) error {
	if err := s.rules.FindOne(ctx, bson.M{"_id": id}).Decode(result); err != nil {
		if err == mongo.ErrNoDocuments {
			return database.ErrNotFound
		}
		return err
	}
	return nil
}

func (s *alertingStore) FindUpdate(ctx context.Context, ruleID string, updateID string, result interface{}) error {
	if err := s.updates.FindOne(ctx, bson.M{"_id": updateID, "rule_id": ruleID}).Decode(result); err != nil {
		if err == mongo.ErrNoDocuments {
			return database.ErrNotFound
		}
		return err
	}
	return nil
}

func (s *alertingStore) FindUpdates(ctx context.Context, ruleID string, limit int, results interface{}) error {
	cursor, err := s.updates.Find(ctx, bson.M{"rule_id": ruleID}, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(int64(limit)))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *alertingStore) FindRuntimeRules(ctx context.Context, endpointID string, signalType string, results interface{}) error {
	query := withAlertSignalFilter(bson.M{"spec.scope.endpoint_id": endpointID}, signalType)
	cursor, err := s.rules.Find(ctx, query, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *alertingStore) MarkRuntimeRulesApplied(ctx context.Context, endpointID string, signalType string, appliedAt time.Time) (int64, error) {
	query := withAlertSignalFilter(bson.M{"spec.scope.endpoint_id": endpointID}, signalType)
	result, err := s.rules.UpdateMany(ctx, query, mongo.Pipeline{
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "apply_status", Value: "applied"},
			{Key: "applied_update_id", Value: "$current_update_id"},
			{Key: "updated_at", Value: appliedAt},
		}}},
	})
	if err != nil {
		return 0, err
	}
	return result.MatchedCount, nil
}

func withAlertSignalFilter(query bson.M, signalType string) bson.M {
	signalType = strings.ToLower(strings.TrimSpace(signalType))
	if signalType == "" {
		return query
	}
	if signalType == "logs" {
		query["$or"] = []bson.M{
			{"spec.signal_type": "logs"},
			{"spec.signal_type": ""},
			{"spec.signal_type": bson.M{"$exists": false}},
		}
		return query
	}
	query["spec.signal_type"] = signalType
	return query
}

func (s *alertingStore) ApplyAlertEvent(ctx context.Context, instance interface{}, event interface{}) error {
	instanceDocument, err := toBSONMap(instance)
	if err != nil {
		return err
	}
	eventDocument, err := toBSONMap(event)
	if err != nil {
		return err
	}
	fingerprint := instanceDocument["_id"]
	session, err := s.client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		var previous bson.M
		if err := s.instances.FindOne(tx, bson.M{"_id": fingerprint}).Decode(&previous); err == nil {
			eventDocument["previous_state"] = previous["state"]
		} else if err != mongo.ErrNoDocuments {
			return nil, err
		}
		if _, err := s.events.InsertOne(tx, eventDocument); err != nil && !mongo.IsDuplicateKeyError(err) {
			return nil, err
		}
		if _, err := s.instances.ReplaceOne(tx, bson.M{"_id": fingerprint}, instance, options.Replace().SetUpsert(true)); err != nil {
			return nil, err
		}
		return nil, nil
	})
	return err
}

func (s *alertingStore) FindAlertInstances(ctx context.Context, ruleID string, serviceID string, state string, limit int, results interface{}) error {
	query := bson.M{}
	if ruleID != "" {
		query["rule_id"] = ruleID
	}
	if serviceID != "" {
		query["service_id"] = serviceID
	}
	if state != "" {
		query["state"] = state
	}
	find := options.Find().SetSort(bson.D{{Key: "last_received_at", Value: -1}})
	if limit > 0 {
		find.SetLimit(int64(limit))
	}
	cursor, err := s.instances.Find(ctx, query, find)
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *alertingStore) FindAlertEvents(ctx context.Context, ruleID string, fingerprint string, limit int, results interface{}) error {
	query := bson.M{}
	if ruleID != "" {
		query["rule_id"] = ruleID
	}
	if fingerprint != "" {
		query["fingerprint"] = fingerprint
	}
	find := options.Find().SetSort(bson.D{{Key: "received_at", Value: -1}})
	if limit > 0 {
		find.SetLimit(int64(limit))
	}
	cursor, err := s.events.Find(ctx, query, find)
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *alertingStore) SaveNotificationPolicy(ctx context.Context, expectedUpdatedAt time.Time, policy interface{}, auditEvent interface{}) error {
	policyDocument, err := toBSONMap(policy)
	if err != nil {
		return err
	}
	policyID := policyDocument["_id"]
	session, err := s.client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		if expectedUpdatedAt.IsZero() {
			if _, err := s.policies.InsertOne(tx, policy); err != nil {
				if mongo.IsDuplicateKeyError(err) {
					return nil, database.ErrConflict
				}
				return nil, err
			}
		} else {
			result, err := s.policies.ReplaceOne(tx, bson.M{"_id": policyID, "updated_at": expectedUpdatedAt}, policy)
			if err != nil {
				if mongo.IsDuplicateKeyError(err) {
					return nil, database.ErrConflict
				}
				return nil, err
			}
			if result.MatchedCount == 0 {
				return nil, database.ErrConflict
			}
		}
		if _, err := s.audits.InsertOne(tx, auditEvent); err != nil {
			return nil, err
		}
		return nil, nil
	})
	return err
}

func (s *alertingStore) FindNotificationPolicyByID(ctx context.Context, id string, result interface{}) error {
	if err := s.policies.FindOne(ctx, bson.M{"_id": id}).Decode(result); err != nil {
		if err == mongo.ErrNoDocuments {
			return database.ErrNotFound
		}
		return err
	}
	return nil
}

func (s *alertingStore) FindNotificationPolicies(ctx context.Context, serviceID string, enabledOnly bool, results interface{}) error {
	query := bson.M{}
	if serviceID != "" {
		query["$or"] = []bson.M{{"service_id": serviceID}, {"service_id": ""}, {"service_id": bson.M{"$exists": false}}}
	}
	if enabledOnly {
		query["enabled"] = true
	}
	cursor, err := s.policies.Find(ctx, query, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

// ---------- RBAC Stores ----------
type rbacRoleStore struct{ col *mongo.Collection }

func (s *rbacRoleStore) Upsert(ctx context.Context, id string, role interface{}) error {
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": id}, role, options.Replace().SetUpsert(true))
	return err
}

func (s *rbacRoleStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *rbacRoleStore) FindByID(ctx context.Context, id string, result interface{}) error {
	return s.col.FindOne(ctx, bson.M{"_id": id}).Decode(result)
}

func (s *rbacRoleStore) Delete(ctx context.Context, id string) error {
	_, err := s.col.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

type rbacBindingStore struct{ col *mongo.Collection }

func (s *rbacBindingStore) Upsert(ctx context.Context, id string, binding interface{}) error {
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": id}, binding, options.Replace().SetUpsert(true))
	return err
}

func (s *rbacBindingStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *rbacBindingStore) FindBySubject(ctx context.Context, subjectID string, subjectType string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"subject_id": subjectID, "subject_type": subjectType}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *rbacBindingStore) Delete(ctx context.Context, id string) error {
	_, err := s.col.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

type platformSubjectStore struct{ col *mongo.Collection }

func (s *platformSubjectStore) Upsert(ctx context.Context, id string, subject interface{}) error {
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": id}, subject, options.Replace().SetUpsert(true))
	return err
}

func (s *platformSubjectStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *platformSubjectStore) Delete(ctx context.Context, id string) error {
	_, err := s.col.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

// ---------- IAM Stores ----------

type iamUserStore struct{ col *mongo.Collection }

func (s *iamUserStore) Upsert(ctx context.Context, id string, user interface{}) error {
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": id}, user, options.Replace().SetUpsert(true))
	return err
}

func (s *iamUserStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *iamUserStore) FindByID(ctx context.Context, id string, result interface{}) error {
	return s.col.FindOne(ctx, bson.M{"_id": id}).Decode(result)
}

func (s *iamUserStore) Delete(ctx context.Context, id string) error {
	_, err := s.col.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

type iamGroupStore struct{ col *mongo.Collection }

func (s *iamGroupStore) Upsert(ctx context.Context, id string, group interface{}) error {
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": id}, group, options.Replace().SetUpsert(true))
	return err
}

func (s *iamGroupStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *iamGroupStore) FindByID(ctx context.Context, id string, result interface{}) error {
	return s.col.FindOne(ctx, bson.M{"_id": id}).Decode(result)
}

func (s *iamGroupStore) Delete(ctx context.Context, id string) error {
	_, err := s.col.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

type iamMembershipStore struct{ col *mongo.Collection }

func (s *iamMembershipStore) Upsert(ctx context.Context, id string, membership interface{}) error {
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": id}, membership, options.Replace().SetUpsert(true))
	return err
}

func (s *iamMembershipStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *iamMembershipStore) FindByGroup(ctx context.Context, groupID string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"group_id": groupID}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *iamMembershipStore) FindBySubject(ctx context.Context, subjectID string, subjectType string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"subject_id": subjectID, "subject_type": subjectType}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *iamMembershipStore) Delete(ctx context.Context, id string) error {
	_, err := s.col.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

type iamServiceAccountStore struct{ col *mongo.Collection }

func (s *iamServiceAccountStore) Upsert(ctx context.Context, id string, serviceAccount interface{}) error {
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": id}, serviceAccount, options.Replace().SetUpsert(true))
	return err
}

func (s *iamServiceAccountStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *iamServiceAccountStore) FindByID(ctx context.Context, id string, result interface{}) error {
	return s.col.FindOne(ctx, bson.M{"_id": id}).Decode(result)
}

func (s *iamServiceAccountStore) Delete(ctx context.Context, id string) error {
	_, err := s.col.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

// ---------- PlatformImageStore ----------
type platformImageStore struct{ col *mongo.Collection }

func (s *platformImageStore) Upsert(ctx context.Context, key string, image interface{}) error {
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": key}, image, options.Replace().SetUpsert(true))
	return err
}

func (s *platformImageStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

// ---------- SecretStore ----------
type secretStore struct{ col *mongo.Collection }

func (s *secretStore) Upsert(ctx context.Context, id string, secret interface{}) error {
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": id}, secret, options.Replace().SetUpsert(true))
	return err
}

func (s *secretStore) FindByID(ctx context.Context, id string, result interface{}) error {
	return s.col.FindOne(ctx, bson.M{"_id": id}).Decode(result)
}

func (s *secretStore) FindByTypeAndScope(ctx context.Context, typ string, scope interface{}, result interface{}) error {
	scopeDoc, err := toBSONMap(scope)
	if err != nil {
		return err
	}
	filter := bson.M{"type": typ}
	for _, key := range []string{"cluster_id", "namespace", "service_id"} {
		if value, ok := scopeDoc[key]; ok && value != "" {
			filter["scope."+key] = value
			continue
		}
		filter["scope."+key] = bson.M{"$in": []any{"", nil}}
	}
	return s.col.FindOne(ctx, filter).Decode(result)
}

func (s *secretStore) FindByType(ctx context.Context, typ string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"type": typ}, options.Find().SetSort(bson.M{"created_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

// ---------- Audit Events ----------
type auditEventStore struct{ col *mongo.Collection }

func (s *auditEventStore) Insert(ctx context.Context, event interface{}) error {
	_, err := s.col.InsertOne(ctx, event)
	return err
}

func (s *auditEventStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"created_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

// ---------- K8s Ops Stores ----------
type k8sClusterStore struct{ col *mongo.Collection }

func (s *k8sClusterStore) Upsert(ctx context.Context, id string, cluster interface{}) error {
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": id}, cluster, options.Replace().SetUpsert(true))
	return err
}

func (s *k8sClusterStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return err
	}
	var docs []bson.M
	if err := cursor.All(ctx, &docs); err != nil {
		return err
	}
	return decodeBSONDocuments(docs, results)
}

func (s *k8sClusterStore) Delete(ctx context.Context, id string) error {
	_, err := s.col.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

type k8sNamespaceStore struct{ col *mongo.Collection }

func (s *k8sNamespaceStore) Upsert(ctx context.Context, id string, namespace interface{}) error {
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": id}, namespace, options.Replace().SetUpsert(true))
	return err
}

func (s *k8sNamespaceStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "cluster_id", Value: 1}, {Key: "name", Value: 1}}))
	if err != nil {
		return err
	}
	var docs []bson.M
	if err := cursor.All(ctx, &docs); err != nil {
		return err
	}
	return decodeBSONDocuments(docs, results)
}

func (s *k8sNamespaceStore) FindByCluster(ctx context.Context, clusterID string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"cluster_id": clusterID}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return err
	}
	var docs []bson.M
	if err := cursor.All(ctx, &docs); err != nil {
		return err
	}
	return decodeBSONDocuments(docs, results)
}

type k8sDeploymentInventoryStore struct{ col *mongo.Collection }

func (s *k8sDeploymentInventoryStore) Upsert(ctx context.Context, id string, record interface{}) error {
	_, err := s.col.ReplaceOne(ctx, bson.M{"_id": id}, record, options.Replace().SetUpsert(true))
	return err
}

func (s *k8sDeploymentInventoryStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "cluster_id", Value: 1}, {Key: "namespace", Value: 1}, {Key: "kind", Value: 1}, {Key: "name", Value: 1}}))
	if err != nil {
		return err
	}
	var docs []bson.M
	if err := cursor.All(ctx, &docs); err != nil {
		return err
	}
	return decodeBSONDocuments(docs, results)
}

func (s *k8sDeploymentInventoryStore) FindByIdentity(ctx context.Context, clusterID string, namespace string, apiVersion string, kind string, name string, result interface{}) error {
	var doc bson.M
	err := s.col.FindOne(ctx, bson.M{
		"cluster_id":  clusterID,
		"namespace":   namespace,
		"api_version": apiVersion,
		"kind":        kind,
		"name":        name,
	}).Decode(&doc)
	if err != nil {
		return err
	}
	return decodeBSONDocument(doc, result)
}

func (s *k8sDeploymentInventoryStore) Delete(ctx context.Context, id string) error {
	result, err := s.col.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if result.DeletedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

type k8sDeploymentHistoryStore struct{ col *mongo.Collection }

func (s *k8sDeploymentHistoryStore) Insert(ctx context.Context, record interface{}) error {
	_, err := s.col.InsertOne(ctx, record)
	return err
}

func (s *k8sDeploymentHistoryStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "started_at", Value: -1}, {Key: "_id", Value: -1}}))
	if err != nil {
		return err
	}
	var docs []bson.M
	if err := cursor.All(ctx, &docs); err != nil {
		return err
	}
	return decodeBSONDocuments(docs, results)
}
