package alerting

import "time"

type Rule struct {
	ID            string    `json:"id" bson:"_id"`
	Name          string    `json:"name" bson:"name"`
	Source        string    `json:"source" bson:"source"`
	RuleType      string    `json:"rule_type" bson:"rule_type"`
	Query         string    `json:"query" bson:"query"`
	Window        string    `json:"window" bson:"window"`
	EvalInterval  string    `json:"eval_interval" bson:"eval_interval"`
	LookbackDelay string    `json:"lookback_delay" bson:"lookback_delay"`
	Condition     string    `json:"condition" bson:"condition"`
	GroupBy       string    `json:"group_by" bson:"group_by"`
	Severity      string    `json:"severity" bson:"severity"`
	OwnerTeam     string    `json:"owner_team" bson:"owner_team"`
	AlertRoute    string    `json:"alert_route" bson:"alert_route"`
	Status        string    `json:"status" bson:"status"`
	CreatedAt     time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" bson:"updated_at"`
}
