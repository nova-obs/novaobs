package secret

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoRepository struct {
	collection *mongo.Collection
}

func NewMongoRepository(db *mongo.Database) MongoRepository {
	return MongoRepository{collection: db.Collection("secrets")}
}

func (r MongoRepository) Save(ctx context.Context, item Secret) error {
	_, err := r.collection.ReplaceOne(ctx, bson.M{"_id": item.ID}, item, options.Replace().SetUpsert(true))
	return err
}

func (r MongoRepository) Get(ctx context.Context, id string) (Secret, error) {
	var item Secret
	err := r.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&item)
	return item, err
}
