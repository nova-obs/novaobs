package logquery

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSearchReturnsEmptyWhenNoEntries(t *testing.T) {
	service := NewService()

	result := service.Search(Query{
		Service:     "payment-gateway-service",
		Environment: "prod",
		Level:       "error",
	})

	require.Len(t, result.Items, 0)
	require.Equal(t, 0, result.Total)
}

func TestSearchWithCustomEntries(t *testing.T) {
	service := Service{entries: []LogEntry{
		{
			Timestamp:   time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC),
			Level:       "error",
			Message:     "支付失败",
			Service:     "payment-gateway-service",
			Environment: "prod",
		},
		{
			Timestamp:   time.Date(2026, 5, 7, 9, 58, 0, 0, time.UTC),
			Level:       "info",
			Message:     "订单创建成功",
			Service:     "order-api",
			Environment: "prod",
		},
	}}

	result := service.Search(Query{
		Service:     "payment-gateway-service",
		Environment: "prod",
		Level:       "error",
	})

	require.Len(t, result.Items, 1)
	require.Equal(t, "payment-gateway-service", result.Items[0].Service)
	require.Equal(t, "error", result.Items[0].Level)
	require.Equal(t, 1, result.Total)
}

func TestSearchFiltersByTimeRange(t *testing.T) {
	service := Service{entries: []LogEntry{
		{
			Timestamp:   time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC),
			Level:       "error",
			Message:     "timeout",
			Service:     "payment-gateway-service",
			Environment: "prod",
		},
		{
			Timestamp:   time.Date(2026, 5, 7, 8, 0, 0, 0, time.UTC),
			Level:       "warn",
			Message:     "old event",
			Service:     "payment-gateway-service",
			Environment: "prod",
		},
	}}

	start := time.Date(2026, 5, 7, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 7, 11, 0, 0, 0, time.UTC)

	result := service.Search(Query{Start: &start, End: &end})

	require.Len(t, result.Items, 1)
	require.Equal(t, "payment-gateway-service", result.Items[0].Service)
}
