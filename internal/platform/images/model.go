package images

import (
	"errors"
	"time"
)

const (
	OTelCollectorImagePlaceholder = "__NOVAAPM_IMAGE_OTEL_COLLECTOR__"
	VmalertImagePlaceholder       = "__NOVAAPM_IMAGE_VMALERT__"
	VMAgentImagePlaceholder       = "__NOVAAPM_IMAGE_VMAGENT__"
)

var DefaultTemplateValues = map[string]string{
	OTelCollectorImagePlaceholder: "hub-test.service.ucloud.cn/logsplatfrom/opentelemetry-collector-contrib:0.153.0",
	VmalertImagePlaceholder:       "hub-test.service.ucloud.cn/logsplatfrom/vmalert:v1.145.0",
	VMAgentImagePlaceholder:       "hub-test.service.ucloud.cn/logsplatfrom/vmagent:v1.145.0",
}

var ErrPermissionDenied = errors.New("permission_denied")

type Image struct {
	Key       string    `json:"key" bson:"_id"`
	Value     string    `json:"value" bson:"value"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

type UpsertRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
