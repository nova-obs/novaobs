package mongo

import (
	"testing"
	"time"

	"novaobs/internal/collectormanagement"

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
