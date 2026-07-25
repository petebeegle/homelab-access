package access

import "time"

type PrincipalID string

type RequestStatus string

const (
	StatusPending      RequestStatus = "pending"
	StatusClaimed      RequestStatus = "claimed"
	StatusProvisioning RequestStatus = "provisioning"
	StatusApproved     RequestStatus = "approved"
	StatusDenied       RequestStatus = "denied"
	StatusFailed       RequestStatus = "failed"
	StatusSuperseded   RequestStatus = "superseded"
)

type Principal struct {
	ID          PrincipalID `json:"id"`
	DisplayName string      `json:"display_name,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

type ApprovalClaim struct {
	ID         string
	Request    Request
	ReviewerID string
	ClaimedAt  time.Time
}

type GrantState string

const (
	GrantStateActive           GrantState = "active"
	GrantStateRevocationQueued GrantState = "revocation_queued"
	GrantStateRevoking         GrantState = "revoking"
	GrantStateRevoked          GrantState = "revoked"
	GrantStateExpired          GrantState = "expired"
	GrantStateRevocationFailed GrantState = "revocation_failed"
)

type Grant struct {
	ID                  string      `json:"id"`
	PrincipalID         PrincipalID `json:"principal_id"`
	RequestID           string      `json:"request_id"`
	State               GrantState  `json:"state"`
	AuthentikUserID     string      `json:"authentik_user_id,omitempty"`
	WireGuardClientID   string      `json:"wireguard_client_id,omitempty"`
	PolicyVersion       string      `json:"policy_version,omitempty"`
	IdentityBrokerOwned bool        `json:"identity_broker_owned"`
	WireGuardManaged    bool        `json:"wireguard_managed"`
	StartsAt            time.Time   `json:"starts_at"`
	ExpiresAt           time.Time   `json:"expires_at,omitempty"`
	RevokedAt           time.Time   `json:"revoked_at,omitempty"`
	CreatedAt           time.Time   `json:"created_at"`
	UpdatedAt           time.Time   `json:"updated_at"`
}

func (g Grant) BlocksNewGrant() bool {
	switch g.State {
	case GrantStateActive, GrantStateRevocationQueued, GrantStateRevoking:
		return true
	default:
		return false
	}
}

type ArtifactState string

const (
	ArtifactStateAvailable ArtifactState = "available"
	ArtifactStateConsumed  ArtifactState = "consumed"
	ArtifactStateExpired   ArtifactState = "expired"
	ArtifactStatePurged    ArtifactState = "purged"
)

type Artifact struct {
	ID         string        `json:"id"`
	GrantID    string        `json:"grant_id"`
	Type       string        `json:"type"`
	State      ArtifactState `json:"state"`
	TokenHash  string        `json:"token_hash,omitempty"`
	ExpiresAt  time.Time     `json:"expires_at,omitempty"`
	ConsumedAt time.Time     `json:"consumed_at,omitempty"`
	PurgedAt   time.Time     `json:"purged_at,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
}

type DeliveryState string

const (
	DeliveryStatePending          DeliveryState = "pending"
	DeliveryStateSent             DeliveryState = "sent"
	DeliveryStateBlocked          DeliveryState = "blocked"
	DeliveryStateRetryableFailure DeliveryState = "retryable_failure"
	DeliveryStatePermanentFailure DeliveryState = "permanent_failure"
)

type Delivery struct {
	ID          string        `json:"id"`
	PrincipalID PrincipalID   `json:"principal_id"`
	Channel     string        `json:"channel"`
	State       DeliveryState `json:"state"`
	Attempt     int           `json:"attempt"`
	NextAttempt time.Time     `json:"next_attempt,omitempty"`
	ErrorClass  string        `json:"error_class,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

type JobState string

const (
	JobStateQueued            JobState = "queued"
	JobStateLeased            JobState = "leased"
	JobStateRunning           JobState = "running"
	JobStateRetryable         JobState = "retryable"
	JobStateSucceeded         JobState = "succeeded"
	JobStatePermanentlyFailed JobState = "permanently_failed"
)

type Job struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	AggregateID    string    `json:"aggregate_id"`
	State          JobState  `json:"state"`
	LeaseOwner     string    `json:"lease_owner,omitempty"`
	LeaseExpiresAt time.Time `json:"lease_expires_at,omitempty"`
	Attempt        int       `json:"attempt"`
	NextRunAt      time.Time `json:"next_run_at,omitempty"`
	IdempotencyKey string    `json:"idempotency_key"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type AuditEvent struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"`
	OccurredAt    time.Time `json:"occurred_at"`
	ActorID       string    `json:"actor_id,omitempty"`
	AggregateID   string    `json:"aggregate_id,omitempty"`
	FromState     string    `json:"from_state,omitempty"`
	ToState       string    `json:"to_state,omitempty"`
	Result        string    `json:"result"`
	Reason        string    `json:"reason,omitempty"`
	CorrelationID string    `json:"correlation_id,omitempty"`
}
