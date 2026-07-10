package images

import "time"

const (
	OTelCollectorImagePlaceholder = "__NOVAAPM_IMAGE_OTEL_COLLECTOR__"
	VmalertImagePlaceholder       = "__NOVAAPM_IMAGE_VMALERT__"
)

var DefaultTemplateValues = map[string]string{
	OTelCollectorImagePlaceholder: "hub-test.service.ucloud.cn/logsplatfrom/opentelemetry-collector-contrib:0.153.0",
	VmalertImagePlaceholder:       "hub-test.service.ucloud.cn/logsplatfrom/vmalert:v1.145.0",
}

type Image struct {
	Key       string    `json:"key" bson:"_id"`
	Value     string    `json:"value" bson:"value"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

type UpsertRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
