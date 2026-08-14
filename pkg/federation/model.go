// Package federation defines Home-owned directory state. Member execution
// data must not be stored through these interfaces.
package federation

import "time"

type MemberStatus string

const (
	MemberOffline MemberStatus = "offline"
	MemberOnline  MemberStatus = "online"
)

type Member struct {
	HomeID             string       `json:"home_id"              db:"home_id"`
	ID                 string       `json:"id"                   db:"id"`
	Enabled            bool         `json:"enabled"              db:"enabled"`
	Status             MemberStatus `json:"status"               db:"status"`
	LastConnectedAt    *time.Time   `json:"last_connected_at"    db:"last_connected_at"`
	LastDisconnectedAt *time.Time   `json:"last_disconnected_at" db:"last_disconnected_at"`
	CreatedAt          time.Time    `json:"created_at"           db:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"           db:"updated_at"`
}

type AuditEvent struct {
	ID        string    `json:"id"         db:"id"`
	HomeID    string    `json:"home_id"    db:"home_id"`
	Type      string    `json:"type"       db:"type"`
	MemberID  string    `json:"member_id"  db:"member_id"`
	ProjectID string    `json:"project_id" db:"project_id"`
	ActorID   string    `json:"actor_id"   db:"actor_id"`
	Detail    string    `json:"detail"     db:"detail"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

const (
	AuditMemberConnected    = "member.connected"
	AuditMemberDisconnected = "member.disconnected"
	AuditProjectCreated     = "project.created"
	AuditProjectOwnerSet    = "project.owner_set"
)
