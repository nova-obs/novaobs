package rbac

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoRepository struct {
	roles    *mongo.Collection
	bindings *mongo.Collection
}

func NewMongoRepository(db *mongo.Database) MongoRepository {
	return MongoRepository{
		roles:    db.Collection("rbac_roles"),
		bindings: db.Collection("rbac_bindings"),
	}
}

func (r MongoRepository) SaveRole(role Role) error {
	_, err := r.roles.ReplaceOne(context.Background(), bson.M{"_id": role.ID}, role, options.Replace().SetUpsert(true))
	return err
}

func (r MongoRepository) GetRole(id string) (Role, error) {
	var role Role
	err := r.roles.FindOne(context.Background(), bson.M{"_id": id}).Decode(&role)
	return role, err
}

func (r MongoRepository) SaveBinding(binding Binding) error {
	_, err := r.bindings.ReplaceOne(context.Background(), bson.M{"_id": binding.ID}, binding, options.Replace().SetUpsert(true))
	return err
}

func (r MongoRepository) ListBindingsBySubject(subjectID string, subjectType string) ([]Binding, error) {
	cursor, err := r.bindings.Find(context.Background(), bson.M{"subject_id": subjectID, "subject_type": subjectType})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())
	var out []Binding
	return out, cursor.All(context.Background(), &out)
}
