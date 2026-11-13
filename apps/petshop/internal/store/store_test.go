package store

import "testing"

func TestOpenInvalidServer(t *testing.T) {
	// 不可达地址：建库失败应返回错误而不是静默成功
	db, err := Open("root:root@tcp(127.0.0.1:1)/?parseTime=true&timeout=200ms", "petshop_not_exist")
	if err == nil {
		t.Fatalf("不可达 MySQL 不应成功，db=%v", db)
	}
}

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
