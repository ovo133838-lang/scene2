// 题目02 宠物门店预约系统 服务入口。
// 配置经启动参数/环境变量注入；密钥不入仓库。
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"petshop/internal/aftercare"
	"petshop/internal/booking"
	"petshop/internal/clock"
	"petshop/internal/httpapi"
	"petshop/internal/payment"
	"petshop/internal/session"
	"petshop/internal/store"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	addr := flag.String("addr", envOr("LISTEN_ADDR", ":8080"), "监听地址")
	mysqlHost := flag.String("mysql", envOr("MYSQL_HOST", "127.0.0.1:3306"), "MySQL 地址")
	mysqlUser := flag.String("mysql-user", envOr("MYSQL_USER", "root"), "MySQL 用户")
	mysqlPass := flag.String("mysql-pass", envOr("MYSQL_PASS", "root"), "MySQL 口令")
	dbName := flag.String("db", envOr("MYSQL_DB", "petshop_t02"), "库名")
	redisAddr := flag.String("redis", envOr("REDIS_ADDR", "127.0.0.1:6379"), "Redis 地址")
	redisPass := flag.String("redis-pass", envOr("REDIS_PASS", ""), "Redis 口令（本机 Redis 实测未设密码，默认空）")
	channelSecret := flag.String("channel-secret", envOr("CHANNEL_SECRET", "demo-channel-secret"), "渠道回调签名密钥")
	payTTL := flag.Duration("pay-ttl", envDuration("PAY_TTL", 5*time.Minute), "待付款占位时长")
	cancelWindow := flag.Duration("cancel-window", envDuration("CANCEL_WINDOW", 2*time.Hour), "提前取消线")
	sweep := flag.Duration("sweep", envDuration("SWEEP_INTERVAL", 5*time.Second), "超时清扫间隔")
	flag.Parse()

	ctx := context.Background()
	clk := clock.Real{}

	dsnServer := *mysqlUser + ":" + *mysqlPass + "@tcp(" + *mysqlHost + ")/?parseTime=true&multiStatements=true&loc=UTC"
	db, err := store.Open(dsnServer, *dbName)
	if err != nil {
		log.Fatalf("连接 MySQL 失败: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		log.Fatalf("Schema 迁移失败: %v", err)
	}
	if err := db.Seed(ctx, clk.Now()); err != nil {
		log.Fatalf("种子数据失败: %v", err)
	}

	sess, err := session.NewRedis(*redisAddr, *redisPass, 12*time.Hour)
	if err != nil {
		log.Fatalf("连接 Redis 失败: %v", err)
	}

	pay := &payment.Service{Conn: db.Conn, Clock: clk, Secret: *channelSecret}
	book := &booking.Service{
		Conn: db.Conn, Clock: clk,
		PayTTL: *payTTL, CancelWindow: *cancelWindow,
		Refunder: pay,
	}
	book.StartSweeper(ctx, *sweep)

	after := &aftercare.Service{Conn: db.Conn, Clock: clk, Refunder: pay}

	srv, err := httpapi.New(db, book, pay, after, sess)
	if err != nil {
		log.Fatalf("装配 HTTP 服务失败: %v", err)
	}
	log.Printf("宠物门店预约系统已启动: http://localhost%s （pay-ttl=%s cancel-window=%s）",
		*addr, *payTTL, *cancelWindow)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatal(err)
	}
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
