package metrics

import (
	"context"
	"strings"

	obsendpoint "novaapm/internal/observability/endpoint"
)

type EndpointCatalog interface {
	Get(ctx context.Context, id string) (obsendpoint.Endpoint, error)
	List(ctx context.Context, filter obsendpoint.ListFilter) ([]obsendpoint.Endpoint, error)
}

type EndpointDestinationReader struct{ catalog EndpointCatalog }

func NewEndpointDestinationReader(catalog EndpointCatalog) EndpointDestinationReader {
	return EndpointDestinationReader{catalog: catalog}
}

func (r EndpointDestinationReader) IsMetricsWriteDestination(ctx context.Context, id string) (bool, error) {
	endpoint, err := r.catalog.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	if endpoint.Kind != obsendpoint.KindVictoriaMetrics || endpoint.Status != "active" || strings.TrimSpace(endpoint.URLs.RemoteWriteURL) == "" {
		return false, nil
	}
	for _, signal := range endpoint.SignalTypes {
		if signal == obsendpoint.SignalTypeMetrics {
			return true, nil
		}
	}
	return false, nil
}

func (r EndpointDestinationReader) ListOptions(ctx context.Context) ([]obsendpoint.Endpoint, error) {
	items, err := r.catalog.List(ctx, obsendpoint.ListFilter{SignalType: obsendpoint.SignalTypeMetrics, Kind: obsendpoint.KindVictoriaMetrics})
	if err != nil {
		return nil, err
	}
	out := make([]obsendpoint.Endpoint, 0, len(items))
	for _, item := range items {
		if item.Status == "active" && strings.TrimSpace(item.URLs.RemoteWriteURL) != "" {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r EndpointDestinationReader) GetOption(ctx context.Context, id string) (obsendpoint.Endpoint, error) {
	return r.catalog.Get(ctx, strings.TrimSpace(id))
}

func (r EndpointDestinationReader) ListDashboardOptions(ctx context.Context) ([]obsendpoint.Endpoint, error) {
	items, err := r.catalog.List(ctx, obsendpoint.ListFilter{Kind: obsendpoint.KindGrafana})
	if err != nil {
		return nil, err
	}
	out := make([]obsendpoint.Endpoint, 0, len(items))
	for _, item := range items {
		if item.Status == "active" && (strings.TrimSpace(item.URLs.UIURL) != "" || strings.TrimSpace(item.URLs.BaseURL) != "") {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r EndpointDestinationReader) IsDashboard(ctx context.Context, id string) (bool, error) {
	item, err := r.catalog.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	return item.Kind == obsendpoint.KindGrafana && item.Status == "active" && (strings.TrimSpace(item.URLs.UIURL) != "" || strings.TrimSpace(item.URLs.BaseURL) != ""), nil
}
