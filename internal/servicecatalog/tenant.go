package servicecatalog

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strconv"
)

const maxTenantID = uint64(1<<32 - 1)

func generateTenantID() (string, error) {
	value, err := rand.Int(rand.Reader, new(big.Int).SetUint64(maxTenantID))
	if err != nil {
		return "", fmt.Errorf("生成产品 ProjectID 失败: %w", err)
	}
	return strconv.FormatUint(value.Uint64()+1, 10), nil
}
