package auth

import (
	"context"

	"github.com/google/uuid"
)

// Principal is the authenticated user for the current request. Handlers must
// take the user ID from here, never from the request body or path.
type Principal struct {
	UserID      uuid.UUID
	SessionID   uuid.UUID
	Email       string
	DisplayName string
	Role        string // "user" | "admin"
}

func (p Principal) IsAdmin() bool { return p.Role == "admin" }

type principalKey struct{}

func withPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the authenticated principal, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// MustPrincipal returns the principal and panics if absent. Only call it in
// handlers mounted behind RequireAuth.
func MustPrincipal(ctx context.Context) Principal {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		panic("auth: MustPrincipal called without an authenticated principal")
	}
	return p
}
