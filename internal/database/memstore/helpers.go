package memstore

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// errNotFound wraps mongo.ErrNoDocuments for errors.Is compatibility.
var errNotFound = mongo.ErrNoDocuments

func newID() string {
	return primitive.NewObjectID().Hex()
}

func extractID(v interface{}) string {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return ""
	}
	idField := rv.FieldByName("ID")
	if idField.IsValid() && idField.Kind() == reflect.String {
		return idField.String()
	}
	return ""
}

func extractServiceID(v interface{}) string {
	return extractStringField(v, "ServiceID")
}

func extractCollectorGroupID(v interface{}) string {
	return extractStringField(v, "CollectorGroupID")
}

func extractConfigHash(v interface{}) string {
	return extractStringField(v, "ConfigHash")
}

func extractStringField(v interface{}, field string) string {
	if m, ok := v.(map[string]interface{}); ok {
		for _, key := range []string{field, bsonLikeFieldName(field), snakeCase(field)} {
			if value, ok := m[key].(string); ok {
				return value
			}
		}
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return ""
	}
	valueField := rv.FieldByName(field)
	if valueField.IsValid() && valueField.Kind() == reflect.String {
		return valueField.String()
	}
	return ""
}

func bsonLikeFieldName(field string) string {
	switch field {
	case "ID":
		return "_id"
	case "ServiceID":
		return "service_id"
	case "CollectorGroupID":
		return "collector_group_id"
	case "ConfigHash":
		return "config_hash"
	default:
		return snakeCase(field)
	}
}

func snakeCase(value string) string {
	var b strings.Builder
	for i, r := range value {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

func copyAll(values map[string]interface{}, results interface{}) error {
	list := make([]interface{}, 0, len(values))
	for _, v := range values {
		list = append(list, v)
	}
	data, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("memstore marshal: %w", err)
	}
	return json.Unmarshal(data, results)
}

func copyValue(value interface{}, result interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("memstore marshal: %w", err)
	}
	return json.Unmarshal(data, result)
}

func mergeUpdates(target interface{}, updates map[string]interface{}) error {
	data, err := json.Marshal(target)
	if err != nil {
		return err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	for k, v := range updates {
		m[k] = v
	}
	data, err = json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func mergedValue(target interface{}, updates map[string]interface{}) (interface{}, error) {
	data, err := json.Marshal(target)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	for k, v := range updates {
		m[k] = v
	}
	return m, nil
}
