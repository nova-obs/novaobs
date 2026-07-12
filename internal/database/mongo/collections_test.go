package mongo

import (
	"testing"
	"time"

	"novaapm/internal/collectormanagement"
	"novaapm/internal/modules/k8sops/cluster"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestObjectIDFilterUsesStringID(t *testing.T) {
	id := "507f1f77bcf86cd799439011"

	value, err := objectID(id)

	require.NoError(t, err)
	require.Equal(t, id, value)
}

func TestDecodeK8sClusterDocumentNormalizesObjectID(t *testing.T) {
	doc := bson.M{
		"_id":    primitive.NewObjectID(),
		"name":   "stage-core",
		"region": "cn-beijing",
		"status": "active",
	}

	var item cluster.Cluster
	require.NoError(t, decodeBSONDocument(doc, &item))

	require.NotEmpty(t, item.ID)
	require.Equal(t, "stage-core", item.Name)
	require.Equal(t, "active", item.Status)
}

func TestDecodeCollectorInstanceDocumentNormalizesObjectID(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	doc := bson.M{
		"_id":                primitive.NewObjectID(),
		"instance_uid":       "collector-a",
		"collector_group_id": "group-a",
		"online":             true,
		"last_seen_at":       now,
	}

	var instance collectormanagement.CollectorInstance
	require.NoError(t, decodeBSONDocument(doc, &instance))

	require.NotEmpty(t, instance.ID)
	require.Equal(t, "collector-a", instance.InstanceUID)
	require.Equal(t, "group-a", instance.CollectorGroupID)
	require.True(t, instance.Online)
	require.True(t, instance.LastSeenAt.Equal(now))
}

func TestCollectorInstanceUpdateDocumentOmitsID(t *testing.T) {
	instance := collectormanagement.CollectorInstance{
		ID:               "collector-a",
		InstanceUID:      "collector-a",
		CollectorGroupID: "",
		Online:           false,
	}

	doc, err := toBSONMap(instance)
	require.NoError(t, err)
	delete(doc, "_id")

	require.NotContains(t, doc, "_id")
	require.Equal(t, "collector-a", doc["instance_uid"])
	require.Equal(t, "", doc["collector_group_id"])
}

func TestCollectorInstanceUpsertDocumentPreservesOpAMPInstanceUID(t *testing.T) {
	instance := collectormanagement.CollectorInstance{
		ID:               "runtime-identity-key",
		InstanceUID:      "runtime-identity-key",
		OpAMPInstanceUID: "opamp-uid-b",
		RuntimeIdentity:  "k8s:test03:group-001:node-01",
		CollectorGroupID: "old-group",
		Online:           true,
	}

	_, doc, insertID, err := collectorInstanceUpsertDocument("runtime-identity-key", "group-001", instance)

	require.NoError(t, err)
	require.Equal(t, "runtime-identity-key", insertID)
	require.Equal(t, "runtime-identity-key", doc["instance_uid"])
	require.Equal(t, "opamp-uid-b", doc["opamp_instance_uid"])
	require.Equal(t, "group-001", doc["collector_group_id"])
	require.Equal(t, "k8s:test03:group-001:node-01", doc["runtime_identity"])
}

func TestScopedUpdateDocumentMovesIDToSetOnInsert(t *testing.T) {
	doc, insertID, err := scopedUpsertDocuments(collectormanagement.CollectorConfigVersion{
		ID:               "version-1",
		CollectorGroupID: "group-a",
		Status:           "pending",
	}, "fallback-id")

	require.NoError(t, err)
	require.Equal(t, "version-1", insertID)
	require.NotContains(t, doc, "_id")
	require.Equal(t, "pending", doc["status"])
}

func TestCatalogServiceIndexKeepsProductServiceIdentityUnique(t *testing.T) {
	model := catalogServiceIndexModel()

	require.Equal(t, "uniq_product_service_name", *model.Options.Name)
	require.True(t, *model.Options.Unique)
	require.Nil(t, model.Options.PartialFilterExpression)
	require.Equal(t, bson.D{{Key: "product_id", Value: 1}, {Key: "name", Value: 1}}, model.Keys)
}

func TestVMLogAgentEndpointIndexKeepsRouteAddressUnique(t *testing.T) {
	model := vmLogAgentEndpointIndexModel()

	require.Equal(t, "uniq_vm_log_route_address", *model.Options.Name)
	require.True(t, *model.Options.Unique)
	require.Equal(t, bson.D{{Key: "route_id", Value: 1}, {Key: "address", Value: 1}}, model.Keys)
}
