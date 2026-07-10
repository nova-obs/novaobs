package images

import (
	"context"
	"errors"
	"strings"
	"testing"

	"novaapm/internal/database/memstore"
)

func TestServiceListsDefaultImages(t *testing.T) {
	svc := NewService(NewStoreRepository(memstore.NewStore().PlatformImages()))

	items, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 default images, got %d", len(items))
	}
	values := map[string]string{}
	for _, item := range items {
		values[item.Key] = item.Value
	}
	if !strings.Contains(values[OTelCollectorImagePlaceholder], "opentelemetry-collector-contrib:0.153.0") {
		t.Fatalf("unexpected collector image value: %s", values[OTelCollectorImagePlaceholder])
	}
	if !strings.Contains(values[VmalertImagePlaceholder], "vmalert:v1.145.0") {
		t.Fatalf("unexpected vmalert image value: %s", values[VmalertImagePlaceholder])
	}
}

func TestServiceUpsertOverridesDefaultImage(t *testing.T) {
	svc := NewService(NewStoreRepository(memstore.NewStore().PlatformImages()))

	_, err := svc.Upsert(context.Background(), UpsertRequest{
		Key:   OTelCollectorImagePlaceholder,
		Value: "harbor.example.com/novaapm/opentelemetry-collector-contrib:0.153.0",
	})
	if err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}

	values, err := svc.TemplateValues(context.Background())
	if err != nil {
		t.Fatalf("TemplateValues returned error: %v", err)
	}
	if values[OTelCollectorImagePlaceholder] != "harbor.example.com/novaapm/opentelemetry-collector-contrib:0.153.0" {
		t.Fatalf("override not applied: %s", values[OTelCollectorImagePlaceholder])
	}
}

func TestServiceRejectsUnknownPlaceholder(t *testing.T) {
	svc := NewService(NewStoreRepository(memstore.NewStore().PlatformImages()))

	_, err := svc.Upsert(context.Background(), UpsertRequest{Key: "__UNKNOWN_IMAGE__", Value: "example.com/image:tag"})
	if err == nil {
		t.Fatal("expected unknown placeholder to be rejected")
	}
}

func TestServiceReturnsUnavailableWhenRepositoryMissing(t *testing.T) {
	svc := NewService(nil)

	if _, err := svc.List(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected List unavailable error, got %v", err)
	}
	if _, err := svc.Upsert(context.Background(), UpsertRequest{Key: OTelCollectorImagePlaceholder, Value: "example.com/image:tag"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected Upsert unavailable error, got %v", err)
	}
	if _, err := svc.TemplateValues(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected TemplateValues unavailable error, got %v", err)
	}
}

func TestStoreRepositoryReturnsUnavailableWhenStoreMissing(t *testing.T) {
	repo := NewStoreRepository(nil)

	if _, err := repo.List(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected List unavailable error, got %v", err)
	}
	if err := repo.Upsert(context.Background(), Image{Key: OTelCollectorImagePlaceholder, Value: "example.com/image:tag"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected Upsert unavailable error, got %v", err)
	}
}

func TestApplyTemplateValuesReplacesKnownPlaceholders(t *testing.T) {
	rendered := ApplyTemplateValues(
		"image: __NOVAAPM_IMAGE_OTEL_COLLECTOR__\nsidecar: __UNKNOWN_IMAGE__",
		map[string]string{OTelCollectorImagePlaceholder: "harbor.example.com/novaapm/otel:1"},
	)

	if !strings.Contains(rendered, "image: harbor.example.com/novaapm/otel:1") {
		t.Fatalf("known placeholder was not replaced: %s", rendered)
	}
	if !strings.Contains(rendered, "sidecar: __UNKNOWN_IMAGE__") {
		t.Fatalf("unknown placeholder should be left untouched: %s", rendered)
	}
}
