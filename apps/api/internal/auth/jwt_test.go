package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret-key-at-least-32-bytes-long"

// TestGenerateToken verifies token generation produces a valid JWT string.
func TestGenerateToken(t *testing.T) {
	token, err := GenerateToken("user-1", "org-1", "alice@example.com", "admin", testSecret, time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken() error: %v", err)
	}
	if token == "" {
		t.Fatal("GenerateToken() returned empty token")
	}

	// Verify it's a well-formed JWT (3 base64 parts separated by dots)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}
}

// TestValidateToken verifies a valid token is accepted.
func TestValidateToken(t *testing.T) {
	token, err := GenerateToken("user-1", "org-1", "alice@example.com", "admin", testSecret, time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken() error: %v", err)
	}

	claims, err := ValidateToken(token, testSecret)
	if err != nil {
		t.Fatalf("ValidateToken() error: %v", err)
	}
	if claims == nil {
		t.Fatal("ValidateToken() returned nil claims")
	}

	if claims.UserID != "user-1" {
		t.Errorf("UserID = %q, want user-1", claims.UserID)
	}
	if claims.OrgID != "org-1" {
		t.Errorf("OrgID = %q, want org-1", claims.OrgID)
	}
	if claims.Email != "alice@example.com" {
		t.Errorf("Email = %q, want alice@example.com", claims.Email)
	}
	if claims.Role != "admin" {
		t.Errorf("Role = %q, want admin", claims.Role)
	}
}

// TestValidateToken_Expired rejects expired tokens.
func TestValidateToken_Expired(t *testing.T) {
	// Generate a token that expired 1 hour ago
	token, err := GenerateToken("user-1", "org-1", "alice@example.com", "admin", testSecret, -time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken() error: %v", err)
	}

	claims, err := ValidateToken(token, testSecret)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if claims != nil {
		t.Errorf("expected nil claims for expired token, got %+v", claims)
	}
}

// TestValidateToken_Tampered rejects tampered tokens.
func TestValidateToken_Tampered(t *testing.T) {
	token, err := GenerateToken("user-1", "org-1", "alice@example.com", "admin", testSecret, time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken() error: %v", err)
	}

	// Tamper with the payload (middle part)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("expected 3 JWT parts")
	}
	// Modify the payload slightly - flip some bits
	tamperedPayload := parts[1]
	if len(tamperedPayload) > 0 {
		// Change the first char to something else
		tamperedPayload = string('X') + tamperedPayload[1:]
	}
	tamperedToken := parts[0] + "." + tamperedPayload + "." + parts[2]

	claims, err := ValidateToken(tamperedToken, testSecret)
	if err == nil {
		t.Fatal("expected error for tampered token, got nil")
	}
	if claims != nil {
		t.Errorf("expected nil claims for tampered token, got %+v", claims)
	}
}

// TestValidateToken_WrongSecret rejects tokens signed with wrong secret.
func TestValidateToken_WrongSecret(t *testing.T) {
	token, err := GenerateToken("user-1", "org-1", "alice@example.com", "admin", testSecret, time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken() error: %v", err)
	}

	wrongSecret := "wrong-secret-key-at-least-32-bytes-long"
	claims, err := ValidateToken(token, wrongSecret)
	if err == nil {
		t.Fatal("expected error for token with wrong secret, got nil")
	}
	if claims != nil {
		t.Errorf("expected nil claims for wrong secret, got %+v", claims)
	}
}

// TestValidateToken_InvalidFormat rejects malformed tokens.
func TestValidateToken_InvalidFormat(t *testing.T) {
	tests := []string{
		"",
		"not-a-token",
		"only.two.parts",
		"too.many.parts.here.now",
		"header..signature",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			claims, err := ValidateToken(tt, testSecret)
			if err == nil {
				t.Fatal("expected error for invalid token format")
			}
			if claims != nil {
				t.Errorf("expected nil claims, got %+v", claims)
			}
		})
	}
}

// TestGenerateToken_DifferentUsers generates tokens for different users.
func TestGenerateToken_DifferentUsers(t *testing.T) {
	users := []struct {
		userID string
		orgID  string
		email  string
		role   string
	}{
		{"user-1", "org-a", "alice@example.com", "admin"},
		{"user-2", "org-a", "bob@example.com", "member"},
		{"user-3", "org-b", "charlie@example.com", "owner"},
	}

	for _, u := range users {
		t.Run(u.email, func(t *testing.T) {
			token, err := GenerateToken(u.userID, u.orgID, u.email, u.role, testSecret, time.Hour)
			if err != nil {
				t.Fatalf("GenerateToken() error: %v", err)
			}

			claims, err := ValidateToken(token, testSecret)
			if err != nil {
				t.Fatalf("ValidateToken() error: %v", err)
			}

			if claims.UserID != u.userID {
				t.Errorf("UserID = %q, want %q", claims.UserID, u.userID)
			}
			if claims.OrgID != u.orgID {
				t.Errorf("OrgID = %q, want %q", claims.OrgID, u.orgID)
			}
			if claims.Email != u.email {
				t.Errorf("Email = %q, want %q", claims.Email, u.email)
			}
			if claims.Role != u.role {
				t.Errorf("Role = %q, want %q", claims.Role, u.role)
			}
		})
	}
}

// TestGenerateToken_ClaimsContainExpiry verifies the token contains time claims.
func TestGenerateToken_ClaimsContainExpiry(t *testing.T) {
	before := time.Now()
	token, err := GenerateToken("user-1", "org-1", "alice@example.com", "admin", testSecret, time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken() error: %v", err)
	}

	claims, err := ValidateToken(token, testSecret)
	if err != nil {
		t.Fatalf("ValidateToken() error: %v", err)
	}

	if claims.ExpiresAt == nil {
		t.Fatal("ExpiresAt is nil")
	}
	if claims.IssuedAt == nil {
		t.Fatal("IssuedAt is nil")
	}
	if claims.NotBefore == nil {
		t.Fatal("NotBefore is nil")
	}

	// Expiry should be in the future (we just created it)
	if claims.ExpiresAt.Before(before) {
		t.Error("ExpiresAt should be in the future")
	}

	// IssuedAt should be near now
	if claims.IssuedAt.After(time.Now().Add(time.Minute)) {
		t.Error("IssuedAt should be near now")
	}
}

// TestGenerateToken_ShortExpiry generates a token with short expiry.
func TestGenerateToken_ShortExpiry(t *testing.T) {
	token, err := GenerateToken("user-1", "org-1", "alice@example.com", "admin", testSecret, time.Second)
	if err != nil {
		t.Fatalf("GenerateToken() error: %v", err)
	}

	// Should be valid immediately
	claims, err := ValidateToken(token, testSecret)
	if err != nil {
		t.Fatalf("ValidateToken() error immediately after generation: %v", err)
	}
	if claims == nil {
		t.Fatal("claims should not be nil")
	}

	// Wait for expiry
	time.Sleep(2 * time.Second)

	claims, err = ValidateToken(token, testSecret)
	if err == nil {
		t.Fatal("expected error after token expiry")
	}
	if claims != nil {
		t.Errorf("expected nil claims after expiry, got %+v", claims)
	}
}

// TestUserFromContext extracts user from context.
func TestUserFromContext(t *testing.T) {
	claims := &Claims{
		UserID: "user-1",
		OrgID:  "org-1",
		Email:  "alice@example.com",
		Role:   "admin",
	}

	ctx := WithUser(context.Background(), claims)

	got := UserFromContext(ctx)
	if got == nil {
		t.Fatal("UserFromContext() returned nil")
	}
	if got.UserID != claims.UserID {
		t.Errorf("UserID = %q, want %q", got.UserID, claims.UserID)
	}
	if got.OrgID != claims.OrgID {
		t.Errorf("OrgID = %q, want %q", got.OrgID, claims.OrgID)
	}
	if got.Email != claims.Email {
		t.Errorf("Email = %q, want %q", got.Email, claims.Email)
	}
	if got.Role != claims.Role {
		t.Errorf("Role = %q, want %q", got.Role, claims.Role)
	}
}

// TestUserFromContext_EmptyContext returns nil from empty context.
func TestUserFromContext_EmptyContext(t *testing.T) {
	ctx := context.Background()
	got := UserFromContext(ctx)
	if got != nil {
		t.Errorf("expected nil from empty context, got %+v", got)
	}
}

// TestUserFromContext_WrongType returns nil when context has wrong type.
func TestUserFromContext_WrongType(t *testing.T) {
	// Store a string instead of *Claims
	ctx := context.WithValue(context.Background(), userContextKey, "not-claims")
	got := UserFromContext(ctx)
	if got != nil {
		t.Errorf("expected nil when wrong type in context, got %+v", got)
	}
}

// TestWithUser stores user in context.
func TestWithUser(t *testing.T) {
	claims := &Claims{
		UserID: "user-1",
		OrgID:  "org-1",
		Email:  "alice@example.com",
		Role:   "admin",
	}

	ctx := WithUser(context.Background(), claims)

	// Verify the claims are stored
	val := ctx.Value(userContextKey)
	if val == nil {
		t.Fatal("claims not stored in context")
	}

	stored, ok := val.(*Claims)
	if !ok {
		t.Fatalf("expected *Claims in context, got %T", val)
	}
	if stored.UserID != claims.UserID {
		t.Errorf("stored UserID = %q, want %q", stored.UserID, claims.UserID)
	}
}

// TestWithUser_NilClaims stores nil claims.
func TestWithUser_NilClaims(t *testing.T) {
	ctx := WithUser(context.Background(), nil)
	got := UserFromContext(ctx)
	if got != nil {
		t.Errorf("expected nil claims, got %+v", got)
	}
}

// TestClaims_RegisteredClaims verifies JWT registered claims work.
func TestClaims_RegisteredClaims(t *testing.T) {
	claims := Claims{
		UserID: "user-1",
		OrgID:  "org-1",
		Email:  "alice@example.com",
		Role:   "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-1",
			Issuer:    "test-issuer",
			Audience:  jwt.ClaimStrings{"test-audience"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	parsed, err := ValidateToken(tokenString, testSecret)
	if err != nil {
		t.Fatalf("ValidateToken() error: %v", err)
	}

	if parsed.Subject != "user-1" {
		t.Errorf("Subject = %q, want user-1", parsed.Subject)
	}
	if parsed.Issuer != "test-issuer" {
		t.Errorf("Issuer = %q, want test-issuer", parsed.Issuer)
	}
}

// TestValidateToken_WrongSigningMethod rejects tokens with unexpected signing method.
func TestValidateToken_WrongSigningMethod(t *testing.T) {
	// Create a token with none signing method (insecure, but for testing)
	token := jwt.New(jwt.SigningMethodNone)
	token.Claims = &Claims{
		UserID: "user-1",
		OrgID:  "org-1",
		Email:  "alice@example.com",
		Role:   "admin",
	}

	// SigningMethodNone requires unsafe allowance
	jwtTokenString, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	claims, err := ValidateToken(jwtTokenString, testSecret)
	if err == nil {
		t.Fatal("expected error for none signing method")
	}
	if claims != nil {
		t.Errorf("expected nil claims, got %+v", claims)
	}
}
