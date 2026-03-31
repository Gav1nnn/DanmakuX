package auth

import (
	"testing"
	"time"
)

func TestGenerateAndParseToken(t *testing.T) {
	secret := "s3cr3t"
	token, err := GenerateToken(secret, "u1", time.Minute)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	claims, err := ParseToken(secret, token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if claims.UserID != "u1" {
		t.Fatalf("unexpected user id: %s", claims.UserID)
	}
}
