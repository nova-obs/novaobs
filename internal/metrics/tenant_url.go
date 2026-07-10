package metrics

import (
	"fmt"
	"net/url"
	"strings"
)

func resolveVictoriaMetricsTenantURL(rawURL string, accountID string, projectID string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("VictoriaMetrics 地址无效")
	}
	segments := strings.Split(parsed.Path, "/")
	resolved := false
	for index := 0; index+1 < len(segments); index++ {
		if segments[index] != "select" && segments[index] != "insert" {
			continue
		}
		segments[index+1] = accountID + ":" + projectID
		resolved = true
		break
	}
	if !resolved {
		return "", fmt.Errorf("多租户指标只支持 VictoriaMetrics Cluster 的 select/insert 地址")
	}
	parsed.Path = strings.Join(segments, "/")
	return parsed.String(), nil
}
