package environment

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoreResourceValidatorAcceptsOnlyExistingUsableResources(t *testing.T) {
	validator := NewStoreResourceValidator(
		stubClusterStore{records: []resourceRecord{{ID: "cluster-active", Status: "active"}, {ID: "cluster-disabled", Status: "disabled"}}},
		stubHostGroupStore{records: map[string]resourceRecord{"host-active": {ID: "host-active", Status: "active"}}},
	)

	require.NoError(t, validator.Validate(context.Background(), ResourceKindK8sCluster, "cluster-active"))
	require.NoError(t, validator.Validate(context.Background(), ResourceKindHostGroup, "host-active"))
	require.ErrorContains(t, validator.Validate(context.Background(), ResourceKindK8sCluster, "missing"), "不存在")
	require.ErrorContains(t, validator.Validate(context.Background(), ResourceKindK8sCluster, "cluster-disabled"), "不可用")
}

type stubClusterStore struct{ records []resourceRecord }

func (s stubClusterStore) Upsert(context.Context, string, interface{}) error { return nil }
func (s stubClusterStore) Delete(context.Context, string) error              { return nil }
func (s stubClusterStore) FindAll(_ context.Context, result interface{}) error {
	items, ok := result.(*[]resourceRecord)
	if !ok {
		return errors.New("结果类型无效")
	}
	*items = append([]resourceRecord(nil), s.records...)
	return nil
}

type stubHostGroupStore struct{ records map[string]resourceRecord }

func (s stubHostGroupStore) Insert(context.Context, interface{}) error  { return nil }
func (s stubHostGroupStore) FindAll(context.Context, interface{}) error { return nil }
func (s stubHostGroupStore) Update(context.Context, string, interface{}) error {
	return nil
}
func (s stubHostGroupStore) Count(context.Context) (int64, error) { return int64(len(s.records)), nil }
func (s stubHostGroupStore) FindByID(_ context.Context, id string, result interface{}) error {
	record, exists := s.records[id]
	if !exists {
		return errors.New("not found")
	}
	target, ok := result.(*resourceRecord)
	if !ok {
		return errors.New("结果类型无效")
	}
	*target = record
	return nil
}
