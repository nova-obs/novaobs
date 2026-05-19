package secret

import "time"

type Scope struct {
	ClusterID string `json:"cluster_id" bson:"cluster_id"`
	Namespace string `json:"namespace,omitempty" bson:"namespace,omitempty"`
	ServiceID string `json:"service_id,omitempty" bson:"service_id,omitempty"`
}

type Secret struct {
	ID          string    `json:"id" bson:"_id,omitempty"`
	Name        string    `json:"name" bson:"name"`
	Type        string    `json:"type" bson:"type"`
	Scope       Scope     `json:"scope" bson:"scope"`
	Ciphertext  string    `json:"-" bson:"ciphertext"`
	Fingerprint string    `json:"fingerprint" bson:"fingerprint"`
	CreatedBy   string    `json:"created_by" bson:"created_by"`
	CreatedAt   time.Time `json:"created_at" bson:"created_at"`
	RotatedAt   time.Time `json:"rotated_at,omitempty" bson:"rotated_at,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitempty" bson:"expires_at,omitempty"`
}

type CreateRequest struct {
	Name      string
	Type      string
	Scope     Scope
	Plaintext []byte
	CreatedBy string
	ExpiresAt time.Time
}
