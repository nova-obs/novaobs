package alerting

import (
	"context"
	"time"
)

type SignalAwareTester struct {
	logs    QueryTester
	metrics QueryTester
}

func NewSignalAwareTester(logs QueryTester, metrics QueryTester) SignalAwareTester {
	return SignalAwareTester{logs: logs, metrics: metrics}
}

func (t SignalAwareTester) Test(ctx context.Context, req TestRequest) (TestResult, error) {
	switch req.Spec.Normalize().SignalType {
	case SignalTypeLogs:
		if t.logs == nil {
			return TestResult{}, ErrUnavailable
		}
		return t.logs.Test(ctx, req)
	case SignalTypeMetrics:
		if t.metrics == nil {
			return TestResult{}, ErrUnavailable
		}
		return t.metrics.Test(ctx, req)
	default:
		return TestResult{}, invalidSpec("signal_type", "告警信号类型无效")
	}
}

type MetricsCompileOnlyTester struct{}

func (MetricsCompileOnlyTester) Test(_ context.Context, req TestRequest) (TestResult, error) {
	query, err := CompileTestQuery(req.Spec)
	if err != nil {
		return TestResult{}, err
	}
	return TestResult{
		CompiledQuery: query,
		Warnings:      []string{"指标规则测试仅编译 PromQL/MetricsQL，不访问 VictoriaMetrics"},
		TestedAt:      time.Time{},
	}, nil
}
