package alerting

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilePublisherWritesAtomicallyAndReloadsVmalert(t *testing.T) {
	directory := t.TempDir()
	var method string
	client := fakeHTTPDoer(func(request *http.Request) (*http.Response, error) {
		method = request.Method
		body := "ok"
		if request.Method == http.MethodGet {
			body = `{"data":{"groups":[{"rules":[{"labels":{"novaobs_runtime_id":"runtime-a","novaobs_rule_id":"rule-a"}}]}]}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	publisher, err := NewFileArtifactPublisher(FilePublisherConfig{
		RulesDirectory: directory,
		ReloadURL:      "http://vmalert.local/-/reload",
		RulesStatusURL: "http://vmalert.local/api/v1/rules",
		Client:         client,
	})
	require.NoError(t, err)
	artifact := Artifact{RuntimeID: "runtime-a", Hash: strings.Repeat("a", 64), Content: "groups: []\n", RuleIDs: []string{"rule-a"}}

	err = publisher.Publish(context.Background(), artifact)

	require.NoError(t, err)
	require.Equal(t, http.MethodGet, method)
	files, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, files, 1)
	content, err := os.ReadFile(filepath.Join(directory, files[0].Name()))
	require.NoError(t, err)
	require.Equal(t, artifact.Content, string(content))
	require.NotContains(t, files[0].Name(), artifact.RuntimeID)
}

func TestFilePublisherRejectsInvalidReloadURL(t *testing.T) {
	_, err := NewFileArtifactPublisher(FilePublisherConfig{RulesDirectory: t.TempDir(), ReloadURL: "file:///tmp/reload", RulesStatusURL: "http://vmalert.local/api/v1/rules"})
	require.Error(t, err)
}

func TestFilePublisherDoesNotAcceptSubstringAsRuleReadback(t *testing.T) {
	client := fakeHTTPDoer(func(request *http.Request) (*http.Response, error) {
		body := "ok"
		if request.Method == http.MethodGet {
			body = `{"labels":{"novaobs_runtime_id":"runtime","novaobs_rule_id":"rule-ab"}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	publisher, err := NewFileArtifactPublisher(FilePublisherConfig{
		RulesDirectory: t.TempDir(), ReloadURL: "http://vmalert.local/-/reload",
		RulesStatusURL: "http://vmalert.local/api/v1/rules", Client: client,
	})
	require.NoError(t, err)
	err = publisher.Publish(context.Background(), Artifact{RuntimeID: "runtime", Hash: strings.Repeat("a", 64), Content: "groups: []\n", RuleIDs: []string{"rule-a"}})
	require.ErrorContains(t, err, "缺少规则 rule-a")
}

func TestFilePublisherRejectsStaleRuntimeRulesAfterDisable(t *testing.T) {
	client := fakeHTTPDoer(func(request *http.Request) (*http.Response, error) {
		body := "ok"
		if request.Method == http.MethodGet {
			body = `{"data":{"groups":[{"rules":[{"labels":{"novaobs_runtime_id":"runtime","novaobs_rule_id":"stale-rule"}}]}]}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	publisher, err := NewFileArtifactPublisher(FilePublisherConfig{
		RulesDirectory: t.TempDir(), ReloadURL: "http://vmalert.local/-/reload",
		RulesStatusURL: "http://vmalert.local/api/v1/rules", Client: client,
	})
	require.NoError(t, err)

	err = publisher.Publish(context.Background(), Artifact{RuntimeID: "runtime", Hash: strings.Repeat("a", 64), Content: "groups: []\n", RuleIDs: []string{}})

	require.ErrorContains(t, err, "存在未期望规则 stale-rule")
}

func TestFilePublisherIgnoresRulesFromOtherRuntime(t *testing.T) {
	client := fakeHTTPDoer(func(request *http.Request) (*http.Response, error) {
		body := "ok"
		if request.Method == http.MethodGet {
			body = `{"data":{"groups":[{"rules":[{"labels":{"novaobs_runtime_id":"other-runtime","novaobs_rule_id":"rule-a"}}]}]}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	publisher, err := NewFileArtifactPublisher(FilePublisherConfig{
		RulesDirectory: t.TempDir(), ReloadURL: "http://vmalert.local/-/reload",
		RulesStatusURL: "http://vmalert.local/api/v1/rules", Client: client,
	})
	require.NoError(t, err)

	err = publisher.Publish(context.Background(), Artifact{RuntimeID: "runtime", Hash: strings.Repeat("a", 64), Content: "groups: []\n", RuleIDs: []string{}})

	require.NoError(t, err)
}
