// Package store 负责 MySQL 连接、建库、Schema 迁移与预置种子数据。
package store

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/stores/sqlx"

	"petshop/internal/model"
)

//go:embed schema.sql
var schemaSQL string

// DemoSalt 演示口令盐（教学预置，非生产用途，已脱敏说明）。
const DemoSalt = "petshop-t02-demo"

type DB struct {
	Conn sqlx.SqlConn
	Name string
}

// Open 连接 MySQL 服务器，必要时创建业务库并返回连接。
// dsnServer 形如 root:root@tcp(127.0.0.1:3306)/?parseTime=true&multiStatements=true&loc=UTC
func Open(dsnServer, dbName string) (*DB, error) {
	server := sqlx.NewMysql(dsnServer)
	if _, err := server.Exec(
		fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", dbName),
	); err != nil {
		return nil, fmt.Errorf("create database %s: %w", dbName, err)
	}
	// 业务库 DSN：库名插入 "/" 与 "?" 之间
	i := strings.Index(dsnServer, "?")
	if i < 0 {
		return nil, fmt.Errorf("dsn 缺少参数段: %s", dsnServer)
	}
	conn := sqlx.NewMysql(dsnServer[:i] + dbName + dsnServer[i:])
	return &DB{Conn: conn, Name: dbName}, nil
}

// Migrate 执行 Schema（幂等，CREATE TABLE IF NOT EXISTS）。
func (s *DB) Migrate(ctx context.Context) error {
	if _, err := s.Conn.ExecCtx(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

// HashPassword 演示口令哈希：sha256(salt|password)。
func HashPassword(password string) string {
	h := sha256.Sum256([]byte(DemoSalt + "|" + password))
	return hex.EncodeToString(h[:])
}

// Seed 写入预置账号、宠物与未来时段（幂等）。
// 账号均为教学预置演示账号（口令 demo123），不承载真实用户。
func (s *DB) Seed(ctx context.Context, now time.Time) error {
	users := []struct {
		username, role, display string
	}{
		{"manager", model.RoleManager, "王店长"},
		{"reception", model.RoleReception, "李前台"},
		{"staff", model.RoleStaff, "张技师"},
		{"service", model.RoleService, "陈客服"},
		{"u1", model.RoleUser, "用户小赵"},
		{"u2", model.RoleUser, "用户小钱"},
		{"u3", model.RoleUser, "用户小孙"},
	}
	hash := HashPassword("demo123")
	for _, u := range users {
		if _, err := s.Conn.ExecCtx(ctx,
			"INSERT INTO users(username,password_hash,role,display_name,created_at) VALUES(?,?,?,?,?) "+
				"ON DUPLICATE KEY UPDATE id=id",
			u.username, hash, u.role, u.display, now,
		); err != nil {
			return fmt.Errorf("seed user %s: %w", u.username, err)
		}
	}

	pets := []struct {
		owner, name string
	}{
		{"u1", "豆豆"}, {"u2", "毛毛"}, {"u3", "雪球"},
	}
	for _, p := range pets {
		var userID int64
		if err := s.Conn.QueryRowCtx(ctx, &userID,
			"SELECT id FROM users WHERE username=?", p.owner); err != nil {
			return fmt.Errorf("seed pet %s: %w", p.name, err)
		}
		var count int64
		if err := s.Conn.QueryRowCtx(ctx, &count,
			"SELECT COUNT(*) FROM pets WHERE owner_id=? AND name=?", userID, p.name); err != nil {
			return err
		}
		if count == 0 {
			if _, err := s.Conn.ExecCtx(ctx,
				"INSERT INTO pets(owner_id,name) VALUES(?,?)", userID, p.name); err != nil {
				return fmt.Errorf("seed pet %s: %w", p.name, err)
			}
		}
	}

	// 未来时段不足 3 个时补足（每时段 3 名额，1 小时一节，间隔 2 小时）
	var future int64
	if err := s.Conn.QueryRowCtx(ctx, &future,
		"SELECT COUNT(*) FROM time_slots WHERE end_at > ?", now); err != nil {
		return err
	}
	for i := future; i < 3; i++ {
		start := now.Add(time.Duration(i+1) * 2 * time.Hour)
		if _, err := s.Conn.ExecCtx(ctx,
			"INSERT INTO time_slots(start_at,end_at,capacity,status,created_at) VALUES(?,?,?,'open',?)",
			start, start.Add(time.Hour), 3, now); err != nil {
			return fmt.Errorf("seed slot %d: %w", i, err)
		}
	}
	return nil
}

// GetUser 按用户名取账号。
func (s *DB) GetUser(ctx context.Context, username string) (*model.User, error) {
	var u model.User
	err := s.Conn.QueryRowCtx(ctx, &u,
		"SELECT id,username,password_hash,role,display_name FROM users WHERE username=?", username)
	if err == sqlx.ErrNotFound {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// HealthCheck 数据库连通性检查。
func (s *DB) HealthCheck(ctx context.Context) error {
	var one int64
	return s.Conn.QueryRowCtx(ctx, &one, "SELECT 1")
}
