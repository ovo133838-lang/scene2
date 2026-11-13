package store

import "testing"

func TestHashPassword(t *testing.T) {
	a := HashPassword("demo123")
	if len(a) != 64 {
		t.Fatalf("应为 sha256 hex（64 位），got %d", len(a))
	}
	if a != HashPassword("demo123") {
		t.Fatalf("同口令应稳定哈希")
	}
	if a == HashPassword("demo124") {
		t.Fatalf("不同口令不应相同哈希")
	}
}
