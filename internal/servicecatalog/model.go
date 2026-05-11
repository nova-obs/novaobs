package servicecatalog

import "time"

type Service struct {
	ID            string     `json:"id" bson:"_id"`
	CMDBServiceID string     `json:"cmdb_service_id" bson:"cmdb_service_id"`
	BusinessID    string     `json:"business_id" bson:"business_id"`
	ApplicationID string     `json:"application_id" bson:"application_id"`
	Name          string     `json:"name" bson:"name"`
	DisplayName   string     `json:"display_name" bson:"display_name"`
	Description   string     `json:"description" bson:"description"`
	Environment   string     `json:"environment" bson:"environment"`
	Cluster       string     `json:"cluster" bson:"cluster"`
	Namespace     string     `json:"namespace" bson:"namespace"`
	OwnerTeam     string     `json:"owner_team" bson:"owner_team"`
	Owner         string     `json:"owner" bson:"owner"`
	AlertRoute    string     `json:"alert_route" bson:"alert_route"`
	SLOLevel      string     `json:"slo_level" bson:"slo_level"`
	IdentityType  string     `json:"identity_type" bson:"identity_type"`
	Status        string     `json:"status" bson:"status"`
	Source        string     `json:"source" bson:"source"`
	SyncStatus    string     `json:"sync_status" bson:"sync_status"`
	LastSyncedAt  *time.Time `json:"last_synced_at,omitempty" bson:"last_synced_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at" bson:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" bson:"updated_at"`
}

type ListFilter struct {
	Query       string
	Environment string
	Status      string
	Source      string
}

type UpdateRequest struct {
	CMDBServiceID *string `json:"cmdb_service_id"`
	BusinessID    *string `json:"business_id"`
	ApplicationID *string `json:"application_id"`
	Name          *string `json:"name"`
	DisplayName   *string `json:"display_name"`
	Description   *string `json:"description"`
	Environment   *string `json:"environment"`
	Cluster       *string `json:"cluster"`
	Namespace     *string `json:"namespace"`
	OwnerTeam     *string `json:"owner_team"`
	Owner         *string `json:"owner"`
	AlertRoute    *string `json:"alert_route"`
	SLOLevel      *string `json:"slo_level"`
	IdentityType  *string `json:"identity_type"`
	Status        *string `json:"status"`
	Source        *string `json:"source"`
	SyncStatus    *string `json:"sync_status"`
}
