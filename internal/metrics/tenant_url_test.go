package metrics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveVictoriaMetricsTenantURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "query", raw: "http://vmselect:8481/select/0/prometheus", want: "http://vmselect:8481/select/10:11/prometheus"},
		{name: "vmui", raw: "http://vmselect:8481/select/0/vmui/", want: "http://vmselect:8481/select/10:11/vmui/"},
		{name: "remote write", raw: "http://vminsert:8480/insert/0/prometheus/api/v1/write", want: "http://vminsert:8480/insert/10:11/prometheus/api/v1/write"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveVictoriaMetricsTenantURL(tt.raw, "10", "11")
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestResolveVictoriaMetricsTenantURLRejectsSingleNodeURL(t *testing.T) {
	_, err := resolveVictoriaMetricsTenantURL("http://vmsingle:8428/api/v1/query", "10", "11")

	require.ErrorContains(t, err, "Cluster")
}
