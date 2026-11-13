package session

import (
	"testing"
	"time"
)

func TestMemoryStoreLifecycle(t *testing.T) {
	s := NewMemory()
	tok := NewToken()
	if tok == "" {
		t.Fatalf("令牌不应为空")
	}
	if err := s.Set(tok, 7, time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	uid, err := s.Get(tok)
	if err != nil || uid != 7 {
		t.Fatalf("get=%d err=%v", uid, err)
	}
	if err := s.Del(tok); err != nil {
		t.Fatalf("del: %v", err)
	}
	if _, err := s.Get(tok); err == nil {
		t.Fatalf("删除后不应命中")
	}
}

func TestMemoryStoreExpiry(t *testing.T) {
	s := NewMemory()
	tok := NewToken()
	if err := s.Set(tok, 1, -time.Second); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := s.Get(tok); err == nil {
		t.Fatalf("过期会话不应命中")
	}
}

func TestNewTokenUnique(t *testing.T) {
	a, b := NewToken(), NewToken()
	if a == b {
		t.Fatalf("令牌应随机唯一")
	}
	if len(a) != 64 {
		t.Fatalf("令牌应为 32 字节 hex，got %d", len(a))
	}
}
