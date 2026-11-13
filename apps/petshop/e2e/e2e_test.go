// Package e2e 题目02 全链路集成测试：真实 MySQL + 完整 HTTP 栈 + 教学时钟。
// 覆盖所有接口与状态迁移，关键操作均执行两次以验证幂等与拒绝行为。
// 需要本机 MySQL（root/root@127.0.0.1:3306）；不可达时跳过。
package e2e

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"petshop/internal/aftercare"
	"petshop/internal/booking"
	"petshop/internal/clock"
	"petshop/internal/httpapi"
	"petshop/internal/payment"
	"petshop/internal/session"
	"petshop/internal/store"
)

const testSecret = "e2e-secret"

// testDB 每次运行使用独立库名：go-zero sqlx 按 DSN 进程级缓存连接池，
// 同名库 DROP 后复用旧连接池会出现 "No database selected"（Error 1046）。
var testDB = "petshop_t02_e2e"

var (
	baseURL  string
	clk      *clock.Fake
	db       *store.DB
	paySvc   *payment.Service
	bookSvc  *booking.Service
	afterSvc *aftercare.Service
)

func mustSetup(t *testing.T) {
	t.Helper()
	if baseURL != "" {
		return
	}
	dsnServer := "root:root@tcp(127.0.0.1:3306)/?parseTime=true&multiStatements=true&loc=UTC"
	// 每次运行独立库：go-zero sqlx 的连接池按 DSN 进程级缓存，
	// 同名库 DROP/CREATE 后旧连接仍指向已删除 schema，会报 Error 1046。
	testDB = fmt.Sprintf("petshop_t02_e2e_%d", time.Now().UnixNano())
	d, err := store.Open(dsnServer, testDB)
	if err != nil {
		t.Skipf("本机 MySQL 不可用，跳过 E2E: %v", err)
	}
	t.Cleanup(func() { dropTestDB(dsnServer, testDB) })
	db = d
	clk = clock.NewFake(time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC))
	ctx := t.Context()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Seed(ctx, clk.Now()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 迁移与种子需可重复执行（重启/流水线重跑场景）
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate twice: %v", err)
	}
	if err := db.Seed(ctx, clk.Now()); err != nil {
		t.Fatalf("seed twice: %v", err)
	}
	paySvc = &payment.Service{Conn: db.Conn, Clock: clk, Secret: testSecret}
	bookSvc = &booking.Service{
		Conn: db.Conn, Clock: clk,
		PayTTL: 5 * time.Minute, CancelWindow: 2 * time.Hour,
		Refunder: paySvc,
	}
	afterSvc = &aftercare.Service{Conn: db.Conn, Clock: clk, Refunder: paySvc}
	sess := session.NewMemory()
	srv, err := httpapi.New(db, bookSvc, paySvc, afterSvc, sess)
	if err != nil {
		t.Fatalf("httpapi: %v", err)
	}
	ts := httptest.NewServer(srv)
	baseURL = ts.URL
	t.Cleanup(ts.Close)
}

// dropTestDB 用原生 database/sql 清理测试库，绕开 go-zero 的 DSN 连接池缓存。
func dropTestDB(dsnServer, name string) {
	admin, err := sql.Open("mysql", dsnServer)
	if err != nil {
		return
	}
	defer admin.Close()
	_, _ = admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name))
}

// ---------- HTTP 帮助 ----------

type client struct {
	t *testing.T
	h *http.Client
}

func newClient(t *testing.T) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, h: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse // 不跟随重定向，便于断言
	}}}
}

func (c *client) login(username string) {
	c.t.Helper()
	r := c.postForm("/login", url.Values{"username": {username}, "password": {"demo123"}})
	if r.StatusCode != 302 {
		c.t.Fatalf("login %s: expect 302, got %d", username, r.StatusCode)
	}
}

func (c *client) get(path string) *http.Response {
	c.t.Helper()
	r, err := c.h.Get(baseURL + path)
	if err != nil {
		c.t.Fatalf("GET %s: %v", path, err)
	}
	r.Body.Close()
	return r
}

func (c *client) getBody(path string) string {
	c.t.Helper()
	r, err := c.h.Get(baseURL + path)
	if err != nil {
		c.t.Fatalf("GET %s: %v", path, err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

// postForm 不关闭 Body，需要读体时由 readBody 读取并关闭。
func (c *client) postForm(path string, v url.Values) *http.Response {
	c.t.Helper()
	r, err := c.h.PostForm(baseURL+path, v)
	if err != nil {
		c.t.Fatalf("POST %s: %v", path, err)
	}
	return r
}

// readBody 读取并关闭响应体。
func readBody(r *http.Response) string {
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

// postPayNotifyRaw 直接构造带签名渠道回调（支付）。
func postPayNotifyRaw(t *testing.T, c *client, txn, bid string, amount int64, result, sig string) (int, string) {
	t.Helper()
	v := url.Values{
		"txn": {txn}, "booking_id": {bid},
		"amount": {fmt.Sprintf("%d", amount)}, "result": {result},
		"event_time": {fmt.Sprintf("%d", clk.Now().Unix())}, "sig": {sig},
	}
	r := c.postForm("/api/notify", v)
	return r.StatusCode, readBody(r)
}

func signOf(txn, bookingID string, amount int64, result string) string {
	n := payment.Notify{
		ChannelTxn: txn, BookingID: bookingID, Amount: amount,
		Result: result, EventTime: clk.Now(),
	}
	return paySvc.Sign(n)
}

// ---------- 业务帮助 ----------

// bookAndPay 走完整 预约→支付→渠道成功 链路，返回预约号。
func bookAndPay(t *testing.T, c *client, username string, petID, slotID int64) string {
	t.Helper()
	r := c.postForm("/user/book", url.Values{"petId": {fmt.Sprint(petID)}, "slotId": {fmt.Sprint(slotID)}})
	if r.StatusCode != 302 || !strings.Contains(r.Header.Get("Location"), "msg=") {
		t.Fatalf("book: unexpected %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	bid := bookingIDFromUserPage(t, c)
	if bid == "" {
		t.Fatalf("book: 未找到待付款预约")
	}
	payID := initiatePay(t, c, bid)
	c.postForm("/mock/pay", url.Values{"paymentId": {payID}, "action": {"success"}})
	return bid
}

func initiatePay(t *testing.T, c *client, bookingID string) string {
	t.Helper()
	r := c.postForm("/user/pay", url.Values{"bookingId": {bookingID}})
	loc := r.Header.Get("Location")
	if !strings.Contains(loc, "payment") && !strings.Contains(loc, "msg=") {
		t.Fatalf("pay: unexpected %d %s", r.StatusCode, loc)
	}
	// 支付单号从模拟渠道页解析
	page := c.getBody("/mock")
	i := strings.Index(page, `name="paymentId" value="`)
	if i < 0 {
		t.Fatalf("pay: mock 页无支付单")
	}
	rest := page[i+len(`name="paymentId" value="`):]
	return rest[:strings.Index(rest, `"`)]
}

func bookingIDFromUserPage(t *testing.T, c *client) string {
	t.Helper()
	page := c.getBody("/user")
	i := strings.Index(page, `name="bookingId" value="B`)
	if i < 0 {
		return ""
	}
	rest := page[i+len(`name="bookingId" value="`):]
	return rest[:strings.Index(rest, `"`)]
}

func userIDOf(t *testing.T, username string) int64 {
	t.Helper()
	var id int64
	if err := db.Conn.QueryRow(&id, "SELECT id FROM users WHERE username=?", username); err != nil {
		t.Fatalf("userIDOf %s: %v", username, err)
	}
	return id
}

func petIDOf(t *testing.T, username string) int64 {
	t.Helper()
	var id int64
	if err := db.Conn.QueryRow(&id,
		"SELECT p.id FROM pets p JOIN users u ON u.id=p.owner_id WHERE u.username=?", username); err != nil {
		t.Fatalf("petIDOf: %v", err)
	}
	return id
}

func slotIDAt(t *testing.T, n int) int64 {
	t.Helper()
	var id int64
	if err := db.Conn.QueryRow(&id,
		"SELECT id FROM time_slots ORDER BY start_at LIMIT 1 OFFSET ?", n); err != nil {
		t.Fatalf("slotIDAt: %v", err)
	}
	return id
}

func bookingStatus(t *testing.T, id string) string {
	t.Helper()
	var st string
	if err := db.Conn.QueryRow(&st, "SELECT status FROM bookings WHERE id=?", id); err != nil {
		t.Fatalf("bookingStatus: %v", err)
	}
	return st
}

func takenCount(t *testing.T, slotID int64) int64 {
	t.Helper()
	var n int64
	if err := db.Conn.QueryRow(&n, "SELECT COUNT(*) FROM slot_taken WHERE slot_id=?", slotID); err != nil {
		t.Fatal(err)
	}
	return n
}

// ---------- E2E 入口 ----------

func TestAllInterfacesAndStates(t *testing.T) {
	mustSetup(t)
	t.Run("健康检查x2", testHealthTwice)
	t.Run("登录x2与错误口令x2", testLoginTwice)
	t.Run("角色鉴权x2", testRoleAuthTwice)
	t.Run("预约成功与重复预约x2", testBookTwice)
	t.Run("名额耗尽x2", testSlotFullTwice)
	t.Run("已开始时段不可约x2", testSlotStartedTwice)
	t.Run("发起支付幂等x2", testPayIdempotentTwice)
	t.Run("代他人支付被拒x2", testPayForbiddenTwice)
	t.Run("回调验签与金额x2", testNotifyValidationTwice)
	t.Run("交易号冲突x2", testTxnConflictTwice)
	t.Run("支付成功与重复通知x2", testPaySuccessDuplicateTwice)
	t.Run("待付款取消与重复取消x2", testCancelPendingTwice)
	t.Run("取消后重新预约x2", testRebookAfterCancelTwice)
	t.Run("超时关闭与迟到支付x2", testLatePaymentTwice)
	t.Run("提前取消自动退款x2", testAutoCancelRefundTwice)
	t.Run("退款失败重试x2", testRefundRetryTwice)
	t.Run("临近取消审批x2", testCancelReviewTwice)
	t.Run("拒绝后核销x2", testRejectThenCheckinTwice)
	t.Run("核销服务完成x2", testServiceFlowTwice)
	t.Run("未到店标记x2", testNoShowTwice)
	t.Run("店长建时段x2", testCreateSlotTwice)
	t.Run("未收款退款被拒x2", testRefundNoPaymentTwice)
	t.Run("收店汇总x2", testSummaryTwice)
	t.Run("售后投诉批准退款x2", testAftercareApproveTwice)
	t.Run("售后投诉驳回x2", testAftercareRejectTwice)
	t.Run("未完成不可售后x2", testAftercareNotCompletedTwice)
	t.Run("页面冒烟x2", testPagesTwice)
	t.Run("退款回调验签x2", testRefundNotifyTwice)
	t.Run("后台清扫x2", testSweeperTwice)
	t.Run("Redis会话x2", testSessionRedisTwice)
	t.Run("渠道异常路径x2", testChannelErrorPathsTwice)
	t.Run("未知单号映射x2", testUnknownRequestsTwice)
	t.Run("取消后到账退款x2", testLateCancelledRefundTwice)
	t.Run("状态冲突回调x2", testStateConflictNotifyTwice)
}
