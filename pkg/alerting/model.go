package alerting

import "time"

type Source string

const (
	SourceEvent  Source = "event"
	SourceMetric Source = "metric"

	DefaultCooldownSeconds int64 = 300
	MinCooldownSeconds     int64 = 10
)

type Rule struct {
	ID              string     `json:"id"                 db:"id"`
	ProjectID       string     `json:"project_id"         db:"project_id"`
	Name            string     `json:"name"               db:"name"`
	Source          Source     `json:"on"                 db:"source"`
	EventType       string     `json:"event_type,omitempty" db:"event_type"`
	When            string     `json:"when,omitempty"      db:"when_expr"`
	MetricKey       string     `json:"metric_key,omitempty" db:"metric_key"`
	Condition       string     `json:"condition,omitempty"  db:"condition_expr"`
	Notify          []string   `json:"notify"             db:"-"`
	NotifyJSON      string     `json:"-"                  db:"notify_json"`
	CooldownSeconds int64      `json:"cooldown_seconds"   db:"cooldown_seconds"`
	Enabled         bool       `json:"enabled"            db:"enabled"`
	CreatedBy       string     `json:"created_by,omitempty" db:"created_by"`
	LastMatchedAt   *time.Time `json:"last_matched_at,omitempty" db:"last_matched_at"`
	LastAttemptedAt *time.Time `json:"last_attempted_at,omitempty" db:"last_attempted_at"`
	LastSuccessAt   *time.Time `json:"last_success_at,omitempty" db:"last_success_at"`
	LastError       string     `json:"last_error,omitempty" db:"last_error"`
	CreatedAt       time.Time  `json:"created_at"         db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"         db:"updated_at"`
}

type CreateRequest struct {
	Name            string   `json:"name"`
	Source          Source   `json:"on"`
	EventType       string   `json:"event_type,omitempty"`
	When            string   `json:"when,omitempty"`
	MetricKey       string   `json:"metric_key,omitempty"`
	Condition       string   `json:"condition,omitempty"`
	Notify          []string `json:"notify"`
	CooldownSeconds int64    `json:"cooldown_seconds,omitempty"`
	Enabled         *bool    `json:"enabled,omitempty"`
}

type PatchRequest struct {
	Name            *string   `json:"name,omitempty"`
	Source          *Source   `json:"on,omitempty"`
	EventType       *string   `json:"event_type,omitempty"`
	When            *string   `json:"when,omitempty"`
	MetricKey       *string   `json:"metric_key,omitempty"`
	Condition       *string   `json:"condition,omitempty"`
	Notify          *[]string `json:"notify,omitempty"`
	CooldownSeconds *int64    `json:"cooldown_seconds,omitempty"`
	Enabled         *bool     `json:"enabled,omitempty"`
}
