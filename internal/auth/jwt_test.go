package auth

import (
	"testing"
	"time"
)

// TestGenerateAndParseToken 验证令牌生成和解析流程可闭环。
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
