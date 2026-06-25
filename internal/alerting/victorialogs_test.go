package alerting

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestVictoriaLogsTesterUsesTenantHeadersAndParsesGroupedStats(t *testing.T) {
	var received url.Values
	client := fakeHTTPDoer(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "1001", r.Header.Get("AccountID"))
		require.Equal(t, "2001", r.Header.Get("ProjectID"))
		require.NoError(t, r.ParseForm())
		received = r.Form
		header := make(http.Header)
		header.Set("VL-Request-Duration-Seconds", "0.312")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     header,
			Body: io.NopCloser(strings.NewReader(
				"{\"deployment.environment\":\"prod\",\"matches\":\"128\"}\n" +
					"{\"deployment.environment\":\"staging\",\"matches\":\"56\"}\n",
			)),
		}, nil
	})

	tester := NewVictoriaLogsTester(staticTargetResolver{target: QueryTarget{
		QueryURL:  "http://vl.local/select/logsql/query",
		AccountID: "1001", ProjectID: "2001",
	}}, client)
	result, err := tester.Test(context.Background(), TestRequest{
		Spec: validRuleSpec(), RangeStart: time.Date(2026, 6, 22, 7, 55, 0, 0, time.UTC), RangeEnd: time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC),
	})

	require.NoError(t, err)
	require.Equal(t, int64(184), result.MatchedLogCount)
	require.Equal(t, 2, result.EstimatedInstanceCount)
	require.Equal(t, int64(312), result.QueryDurationMillis)
	require.Len(t, result.TopGroups, 2)
	require.Equal(t, "0", received.Get("allow_partial_response"))
	require.Equal(t, "10s", received.Get("timeout"))
	require.NotEmpty(t, received.Get("start"))
	require.NotEmpty(t, received.Get("end"))
}

func TestVictoriaLogsTesterRejectsOversizedResponse(t *testing.T) {
	client := fakeHTTPDoer(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(string(make([]byte, maxVictoriaLogsResponseBytes+1)))),
		}, nil
	})
	tester := NewVictoriaLogsTester(staticTargetResolver{target: QueryTarget{QueryURL: "http://vl.local/select/logsql/query", AccountID: "1", ProjectID: "1"}}, client)

	_, err := tester.Test(context.Background(), TestRequest{Spec: validRuleSpec(), RangeStart: time.Now().Add(-time.Minute), RangeEnd: time.Now()})

	require.ErrorIs(t, err, ErrQueryFailed)
}

type fakeHTTPDoer func(*http.Request) (*http.Response, error)

func (f fakeHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

type staticTargetResolver struct {
	target QueryTarget
	err    error
}

func (r staticTargetResolver) ResolveQueryTarget(context.Context, RuleScope) (QueryTarget, error) {
	return r.target, r.err
}
