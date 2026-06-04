package mongo

import (
	"context"
	"reflect"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ---------- ServiceStore ----------
type svcStore struct{ col *mongo.Collection }

func (s *svcStore) Insert(ctx context.Context, svc interface{}) error {
	_, err := s.col.InsertOne(ctx, svc)
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
	setDoc, err := toBSONMap(instance)
	if err != nil {
		return err
	}
	filter := bson.M{"instance_uid": instanceUID}
	if runtimeIdentity, _ := setDoc["runtime_identity"].(string); strings.TrimSpace(runtimeIdentity) != "" {
		filter = bson.M{"$or": []bson.M{
			{"runtime_identity": runtimeIdentity},
			{"instance_uid": instanceUID},
		}}
	}
	opts := options.Update().SetUpsert(true)
	insertID := normalizeDocumentID(setDoc, instanceUID)
	delete(setDoc, "_id")
	setDoc["instance_uid"] = instanceUID
	setDoc["opamp_instance_uid"] = instanceUID
	setDoc["collector_group_id"] = groupID
	_, err = s.col.UpdateOne(ctx, filter, bson.M{
		"$set":         setDoc,
		"$setOnInsert": bson.M{"_id": insertID},
	}, opts)
	return err
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

type logAgentPlanStore struct{ col *mongo.Collection }

func (s *logAgentPlanStore) Insert(ctx context.Context, plan interface{}) error {
	_, err := s.col.InsertOne(ctx, plan)
	return err
}

func (s *logAgentPlanStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"created_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

func (s *logAgentPlanStore) FindByRoute(ctx context.Context, routeID string, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{"route_id": routeID}, options.Find().SetSort(bson.M{"created_at": -1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}

// ---------- AlertRuleStore ----------
type arStore struct{ col *mongo.Collection }

func (s *arStore) Insert(ctx context.Context, rule interface{}) error {
	_, err := s.col.InsertOne(ctx, rule)
	return err
}
func (s *arStore) FindAll(ctx context.Context, results interface{}) error {
	cursor, err := s.col.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	return cursor.All(ctx, results)
}
func (s *arStore) Count(ctx context.Context) (int64, error) {
	return s.col.CountDocuments(ctx, bson.M{})
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
