package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

const testDSN = "root:root@tcp(127.0.0.1:3306)/?parseTime=true&multiStatements=true&loc=UTC"

func TestOpenInvalidServer(t *testing.T) {
	// 不可达地址：建库失败应返回错误而不是静默成功
	db, err := Open("root:root@tcp(127.0.0.1:1)/?parseTime=true&timeout=200ms", "petshop_not_exist")
	if err == nil {
		t.Fatalf("不可达 MySQL 不应成功，db=%v", db)
	}
}

func TestOpenDsnWithoutParams(t *testing.T) {
	// DSN 缺少 "?" 参数段：应返回明确错误
	db, err := Open("root:root@tcp(127.0.0.1:3306)", "petshop_noparams")
	if err == nil {
		t.Fatalf("缺少参数段应报错，db=%v", db)
	}
}

func TestSeedCreatesFutureSlots(t *testing.T) {
	// 空库上 Seed 应补足 3 个未来时段；重复执行幂等
	db, err := Open(testDSN, "petshop_t02_seedtest")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Now()
	for i := 0; i < 2; i++ {
		if err := db.Seed(context.Background(), now); err != nil {
			t.Fatalf("seed round%d: %v", i+1, err)
		}
	}
	var n int64
	if err := db.Conn.QueryRow(&n,
		"SELECT COUNT(*) FROM time_slots WHERE end_at > ?", now); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n < 3 {
		t.Fatalf("未来时段应≥3，got %d", n)
	}
}

func TestSeedFailsWithoutTables(t *testing.T) {
	// 表缺失时 Seed 应带上下文报错，而不是静默成功
	db, err := Open(testDSN, "petshop_t02_seedfail")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Conn.Exec("SET FOREIGN_KEY_CHECKS=0; DROP TABLE IF EXISTS users; SET FOREIGN_KEY_CHECKS=1"); err != nil {
		t.Fatalf("drop: %v", err)
	}
	err = db.Seed(context.Background(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "seed user") {
		t.Fatalf("users 缺失应报 seed user 错误，got %v", err)
	}
}

func TestMigrateFailsOnDroppedDatabase(t *testing.T) {
	name := "petshop_t02_migdrop"
	db, err := Open(testDSN, name)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("首次迁移应成功: %v", err)
	}
	sys, err := Open(testDSN, "mysql")
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	if _, err := sys.Conn.Exec("DROP DATABASE IF EXISTS " + name); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := db.Migrate(context.Background()); err == nil {
		t.Fatalf("库被删除后迁移应报错")
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
