package access

import "context"

type PrincipalRepository interface {
	GetPrincipal(id PrincipalID) (Principal, error)
}

type RequestRepository interface {
	CreateOrGetPending(input RequestInput) (Request, bool, error)
	GetPending(id string) (Request, error)
	ClaimApproval(id, reviewerID string) (ApprovalClaim, error)
	BeginApprovalProvisioning(claim ApprovalClaim) (Request, error)
	ReleaseApprovalClaim(requestID, claimID string) error
	FailApproval(claim ApprovalClaim) (Request, error)
	FinalizeApproval(claim ApprovalClaim, input ApprovalInput) (Request, error)
	Deny(id, reviewerID string) (Request, error)
}

type GrantRepository interface {
	GetActiveGrant(principalID PrincipalID) (Grant, error)
}

type ArtifactRepository interface {
	GetDownload(token string) (Request, error)
	ConsumeDownload(token string) (Request, error)
}

type Store interface {
	PrincipalRepository
	RequestRepository
	GrantRepository
	ArtifactRepository

	// Approve preserves the original store API while callers migrate to the
	// explicit claim and finalize lifecycle.
	Approve(id string, input ApprovalInput) (Request, error)
}

type Identity struct {
	ID       string
	Username string
}

type IdentityProvider interface {
	EnsureIdentity(context.Context, Principal, Request) (Identity, error)
	RevokeEntitlement(context.Context, Grant) error
}

type VPNPeer struct {
	ID            string
	Configuration string
}

type VPNProvider interface {
	EnsurePeer(context.Context, Principal, Request) (VPNPeer, error)
	RevokePeer(context.Context, Grant) error
}

type DeliveryService interface {
	Deliver(context.Context, Delivery) error
}

type JobRepository interface {
	Enqueue(context.Context, Job) error
}

type AuditSink interface {
	Record(context.Context, AuditEvent) error
}
