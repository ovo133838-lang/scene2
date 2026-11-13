package e2e

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"petshop/internal/model"
	"petshop/internal/session"
)

// ---------- 场景 ----------

func testHealthTwice(t *testing.T) {
	for i := 1; i <= 2; i++ {
		if r := newClient(t).get("/health"); r.StatusCode != 200 {
			t.Fatalf("round%d: health=%d", i, r.StatusCode)
		}
	}
}

func testLoginTwice(t *testing.T) {
	for i := 1; i <= 2; i++ {
		c := newClient(t)
		c.login("u1")
		if page := c.getBody("/user"); !strings.Contains(page, "可约时段") {
			t.Fatalf("round%d: 登录后用户页异常", i)
		}
		bad := newClient(t)
		r := bad.postForm("/login", url.Values{"username": {"u1"}, "password": {"wrong"}})
		if !strings.Contains(r.Header.Get("Location"), "err=") {
			t.Fatalf("round%d: 错误口令应被拒绝", i)
		}
	}
}

func testRoleAuthTwice(t *testing.T) {
	for i := 1; i <= 2; i++ {
		u1 := newClient(t)
		u1.login("u1")
		if r := u1.get("/manager"); r.StatusCode != 403 {
			t.Fatalf("round%d: 用户访问店长页应 403，got %d", i, r.StatusCode)
		}
		if r := u1.get("/front"); r.StatusCode != 403 {
			t.Fatalf("round%d: 用户访问前台页应 403，got %d", i, r.StatusCode)
		}
		if r := u1.get("/service"); r.StatusCode != 403 {
			t.Fatalf("round%d: 用户访问客服页应 403，got %d", i, r.StatusCode)
		}
		rep := newClient(t)
		rep.login("reception")
		if r := rep.get("/user"); r.StatusCode != 403 {
			t.Fatalf("round%d: 前台访问用户页应 403，got %d", i, r.StatusCode)
		}
		svc := newClient(t)
		svc.login("service")
		if r := svc.get("/manager"); r.StatusCode != 403 {
			t.Fatalf("round%d: 客服访问店长页应 403，got %d", i, r.StatusCode)
		}
	}
}

func testBookTwice(t *testing.T) {
	slot := mgrSlot(t, at(2*time.Hour), at(3*time.Hour), 3)
	u1 := newClient(t)
	u1.login("u1")
	bid := bookAt(t, u1, "u1", slot)
	for i := 1; i <= 2; i++ {
		expectErr(t, u1, "/user/book", url.Values{"petId": {fmt.Sprint(petIDOf(t, "u1"))}, "slotId": {fmt.Sprint(slot)}}, "已有有效预约")
	}
	if st := bookingStatus(t, bid); st != model.BookingPendingPayment {
		t.Fatalf("新预约应为待付款，got %s", st)
	}
}

func testSlotFullTwice(t *testing.T) {
	slot := mgrSlot(t, at(3*time.Hour), at(4*time.Hour), 1)
	u1 := newClient(t)
	u1.login("u1")
	bookAt(t, u1, "u1", slot)
	u2 := newClient(t)
	u2.login("u2")
	for i := 1; i <= 2; i++ {
		expectErr(t, u2, "/user/book", url.Values{"petId": {fmt.Sprint(petIDOf(t, "u2"))}, "slotId": {fmt.Sprint(slot)}}, "名额不足")
	}
	if taken := takenCount(t, slot); taken != 1 {
		t.Fatalf("容量 1 的时段占用应为 1，got %d", taken)
	}
}

func testSlotStartedTwice(t *testing.T) {
	slot := mgrSlot(t, at(-30*time.Minute), at(30*time.Minute), 3)
	u1 := newClient(t)
	u1.login("u1")
	for i := 1; i <= 2; i++ {
		expectErr(t, u1, "/user/book", url.Values{"petId": {fmt.Sprint(petIDOf(t, "u1"))}, "slotId": {fmt.Sprint(slot)}}, "已开始")
	}
}

func testPayIdempotentTwice(t *testing.T) {
	slot := mgrSlot(t, at(4*time.Hour), at(5*time.Hour), 3)
	u1 := newClient(t)
	u1.login("u1")
	bid := bookAt(t, u1, "u1", slot)
	pid1 := payAt(t, u1, bid)
	pid2 := payAt(t, u1, bid)
	if pid1 == "" || pid1 != pid2 {
		t.Fatalf("重复发起支付应复用同一支付单: %q vs %q", pid1, pid2)
	}
}

func testPayForbiddenTwice(t *testing.T) {
	slot := mgrSlot(t, at(5*time.Hour), at(6*time.Hour), 3)
	u1 := newClient(t)
	u1.login("u1")
	bid := bookAt(t, u1, "u1", slot)
	u2 := newClient(t)
	u2.login("u2")
	for i := 1; i <= 2; i++ {
		expectErr(t, u2, "/user/pay", url.Values{"bookingId": {bid}}, "无权")
	}
}

func testNotifyValidationTwice(t *testing.T) {
	slot := mgrSlot(t, at(6*time.Hour), at(7*time.Hour), 3)
	u1 := newClient(t)
	u1.login("u1")
	bid := bookAt(t, u1, "u1", slot)
	payAt(t, u1, bid)

	for i := 1; i <= 2; i++ {
		code, body := notify(t, "TXNBAD"+bid[len(bid)-4:], bid, model.AmountCents, "success", "deadbeef")
		if code != 400 || !strings.Contains(body, "sig_invalid") {
			t.Fatalf("round%d: 验签失败应 400/sig_invalid, got %d %s", i, code, body)
		}
		code, body = notify(t, "TXNAMT"+bid[len(bid)-4:], bid, 123, "success", "")
		if code != 400 || !strings.Contains(body, "amount_mismatch") {
			t.Fatalf("round%d: 金额不符应 400/amount_mismatch, got %d %s", i, code, body)
		}
		code, body = notify(t, "TXNUN"+bid[len(bid)-4:], "BUNKNOWN000000000000000", model.AmountCents, "success", "")
		if code != 400 || !strings.Contains(body, "unknown_order") {
			t.Fatalf("round%d: 未知订单应 400/unknown_order, got %d %s", i, code, body)
		}
	}
	if st := bookingStatus(t, bid); st != model.BookingPendingPayment {
		t.Fatalf("失败通知不得改变预约状态，got %s", st)
	}
}

func testTxnConflictTwice(t *testing.T) {
	slotA := mgrSlot(t, at(7*time.Hour), at(8*time.Hour), 3)
	slotB := mgrSlot(t, at(9*time.Hour), at(10*time.Hour), 3)
	u1 := newClient(t)
	u1.login("u1")
	bid1 := bookAt(t, u1, "u1", slotA)
	payAt(t, u1, bid1)
	u2 := newClient(t)
	u2.login("u2")
	bid2 := bookAt(t, u2, "u2", slotB)
	payAt(t, u2, bid2)

	txn := "TXNCONFLICT"
	if code, body := notify(t, txn, bid1, model.AmountCents, "success", ""); code != 200 || !strings.Contains(body, "handled=paid") {
		t.Fatalf("首笔合法通知应成功: %d %s", code, body)
	}
	for i := 1; i <= 2; i++ {
		code, body := notify(t, txn, bid2, model.AmountCents, "success", "")
		if code != 400 || !strings.Contains(body, "归属其他订单") {
			t.Fatalf("round%d: 交易号冲突应 400, got %d %s", i, code, body)
		}
	}
}

func testPaySuccessDuplicateTwice(t *testing.T) {
	slot := mgrSlot(t, at(10*time.Hour), at(11*time.Hour), 3)
	u3 := newClient(t)
	u3.login("u3")
	bid := bookAt(t, u3, "u3", slot)
	payAt(t, u3, bid)
	txn := "TXNDUP" + bid[len(bid)-4:]

	code1, body1 := notify(t, txn, bid, model.AmountCents, "success", "")
	code2, body2 := notify(t, txn, bid, model.AmountCents, "success", "")
	if code1 != 200 || !strings.Contains(body1, "handled=paid") {
		t.Fatalf("首次成功通知异常: %d %s", code1, body1)
	}
	if code2 != 200 || !strings.Contains(body2, "handled=duplicate") {
		t.Fatalf("重复通知应幂等 duplicate: %d %s", code2, body2)
	}
	if st := bookingStatus(t, bid); st != model.BookingConfirmed {
		t.Fatalf("支付后应已确认，got %s", st)
	}
	var paidCount int64
	mustQuery(t, &paidCount, "SELECT COUNT(*) FROM payments WHERE booking_id=? AND status='paid'", bid)
	if paidCount != 1 {
		t.Fatalf("同一订单只能有一笔已支付记录，got %d", paidCount)
	}
}

func testCancelPendingTwice(t *testing.T) {
	slot := mgrSlot(t, at(11*time.Hour), at(12*time.Hour), 3)
	u1 := newClient(t)
	u1.login("u1")
	bid := bookAt(t, u1, "u1", slot)
	expectOK(t, u1, "/user/cancel", url.Values{"bookingId": {bid}}, "已取消")
	expectErr(t, u1, "/user/cancel", url.Values{"bookingId": {bid}}, "不允许")
	if taken := takenCount(t, slot); taken != 0 {
		t.Fatalf("取消后名额应释放，got %d", taken)
	}
}

func testRebookAfterCancelTwice(t *testing.T) {
	slot := mgrSlot(t, at(12*time.Hour), at(13*time.Hour), 3)
	u1 := newClient(t)
	u1.login("u1")
	bid := bookAt(t, u1, "u1", slot)
	expectOK(t, u1, "/user/cancel", url.Values{"bookingId": {bid}}, "已取消")
	// 取消后同宠物同时段可重新预约，且仍受唯一有效约束
	bid2 := bookAt(t, u1, "u1", slot)
	expectErr(t, u1, "/user/book", url.Values{"petId": {fmt.Sprint(petIDOf(t, "u1"))}, "slotId": {fmt.Sprint(slot)}}, "已有有效预约")
	expectOK(t, u1, "/user/cancel", url.Values{"bookingId": {bid2}}, "已取消")
}

func testLatePaymentTwice(t *testing.T) {
	slot := mgrSlot(t, at(13*time.Hour), at(14*time.Hour), 3)
	u2 := newClient(t)
	u2.login("u2")
	bid := bookAt(t, u2, "u2", slot)
	payAt(t, u2, bid)
	txn := "TXNLATE" + bid[len(bid)-4:]

	clk.Advance(6 * time.Minute)
	defer clk.Advance(-6 * time.Minute)
	for i := 1; i <= 2; i++ {
		if _, err := bookSvc.CloseExpired(t.Context()); err != nil {
			t.Fatalf("round%d: CloseExpired: %v", i, err)
		}
	}
	if st := bookingStatus(t, bid); st != model.BookingClosed {
		t.Fatalf("超时应关闭，got %s", st)
	}
	code1, body1 := notify(t, txn, bid, model.AmountCents, "success", "")
	if code1 != 200 || !strings.Contains(body1, "late_anomaly") {
		t.Fatalf("迟到支付应 late_anomaly: %d %s", code1, body1)
	}
	code2, body2 := notify(t, txn, bid, model.AmountCents, "success", "")
	if code2 != 200 || !strings.Contains(body2, "duplicate") {
		t.Fatalf("迟到重复通知应幂等: %d %s", code2, body2)
	}
	if st := bookingStatus(t, bid); st != model.BookingPaymentAnomaly {
		t.Fatalf("应为异常支付，got %s", st)
	}
	var refunds int64
	mustQuery(t, &refunds, "SELECT COUNT(*) FROM refunds WHERE booking_id=?", bid)
	if refunds != 1 {
		t.Fatalf("异常支付应自动发起一条退款，got %d", refunds)
	}
	if taken := takenCount(t, slot); taken != 0 {
		t.Fatalf("异常支付不得恢复名额占用")
	}
}

func testAutoCancelRefundTwice(t *testing.T) {
	slot := mgrSlot(t, at(14*time.Hour), at(15*time.Hour), 3)
	u3 := newClient(t)
	u3.login("u3")
	bid := bookAt(t, u3, "u3", slot)
	payAt(t, u3, bid)
	paySuccess(t, u3, bid)

	expectOK(t, u3, "/user/cancel", url.Values{"bookingId": {bid}}, "自动受理")
	expectErr(t, u3, "/user/cancel", url.Values{"bookingId": {bid}}, "不允许")
	if st := bookingStatus(t, bid); st != model.BookingCancelled {
		t.Fatalf("提前取消应直接取消，got %s", st)
	}
	var refunds int64
	mustQuery(t, &refunds, "SELECT COUNT(*) FROM refunds WHERE booking_id=?", bid)
	if refunds != 1 {
		t.Fatalf("自动取消应产生一条退款，got %d", refunds)
	}
	if st := refundStatusOf(t, bid); st != model.RefundPending {
		t.Fatalf("退款初始应为 pending，got %s", st)
	}
}

func testRefundRetryTwice(t *testing.T) {
	slot := mgrSlot(t, at(16*time.Hour), at(17*time.Hour), 3)
	u3 := newClient(t)
	u3.login("u3")
	bid := bookAt(t, u3, "u3", slot)
	payAt(t, u3, bid)
	paySuccess(t, u3, bid)
	expectOK(t, u3, "/user/cancel", url.Values{"bookingId": {bid}}, "自动受理")
	rid := refundIDOf(t, bid)
	if rid == "" {
		t.Fatalf("自动取消未生成退款单")
	}

	svc := newClient(t)
	svc.login("service")
	svc.postForm("/mock/refund", url.Values{"refundId": {rid}, "action": {"fail"}})
	svc.postForm("/mock/refund", url.Values{"refundId": {rid}, "action": {"fail"}})
	var status string
	mustQuery(t, &status, "SELECT status FROM refunds WHERE id=?", rid)
	if status != model.RefundFailed {
		t.Fatalf("两次失败回调后应保持退款失败，got %s", status)
	}
	// 客服页可见失败退款与重试入口
	if page := svc.getBody("/service"); !strings.Contains(rowHTML(page, rid), "重试退款") {
		t.Fatalf("客服页应展示重试入口")
	}
	expectOK(t, svc, "/service/refund-retry", url.Values{"refundId": {rid}}, "重新发起")
	expectErr(t, svc, "/service/refund-retry", url.Values{"refundId": {rid}}, "不允许")

	svc.postForm("/mock/refund", url.Values{"refundId": {rid}, "action": {"fail"}})
	expectOK(t, svc, "/service/refund-retry", url.Values{"refundId": {rid}}, "重新发起")
	svc.postForm("/mock/refund", url.Values{"refundId": {rid}, "action": {"success"}})
	svc.postForm("/mock/refund", url.Values{"refundId": {rid}, "action": {"success"}})
	mustQuery(t, &status, "SELECT status FROM refunds WHERE id=?", rid)
	if status != model.RefundSuccess {
		t.Fatalf("退款应最终成功，got %s", status)
	}
	if st := bookingStatus(t, bid); st != model.BookingCancelled {
		t.Fatalf("退款完成不得复活预约，got %s", st)
	}
	if page := u3.getBody("/user"); !strings.Contains(rowHTML(page, bid), "已退款") {
		t.Fatalf("用户页应显示已退款")
	}
}

func testCancelReviewTwice(t *testing.T) {
	slot := mgrSlot(t, at(30*time.Minute), at(90*time.Minute), 3)
	u1 := newClient(t)
	u1.login("u1")
	bid := bookAt(t, u1, "u1", slot)
	payAt(t, u1, bid)
	paySuccess(t, u1, bid)

	expectOK(t, u1, "/user/cancel", url.Values{"bookingId": {bid}, "reason": {"临时有事"}}, "客服审核")
	expectErr(t, u1, "/user/cancel", url.Values{"bookingId": {bid}, "reason": {"再提一次"}}, "待审核")
	if st := bookingStatus(t, bid); st != model.BookingCancelReview {
		t.Fatalf("临近取消应转审核，got %s", st)
	}
	// 审核期间不得核销
	rep := newClient(t)
	rep.login("reception")
	for i := 1; i <= 2; i++ {
		expectErr(t, rep, "/front/checkin", url.Values{"bookingId": {bid}}, "不允许")
	}
	svc := newClient(t)
	svc.login("service")
	expectOK(t, svc, "/service/review", url.Values{"bookingId": {bid}, "decision": {"approve"}, "note": {"同意"}}, "批准")
	expectErr(t, svc, "/service/review", url.Values{"bookingId": {bid}, "decision": {"approve"}}, "不允许")
	if st := bookingStatus(t, bid); st != model.BookingCancelled {
		t.Fatalf("批准后应取消，got %s", st)
	}
	if refundIDOf(t, bid) == "" {
		t.Fatalf("批准取消应自动生成退款单")
	}
	if taken := takenCount(t, slot); taken != 0 {
		t.Fatalf("批准后名额应释放，got %d", taken)
	}
}

func testRejectThenCheckinTwice(t *testing.T) {
	slot := mgrSlot(t, at(45*time.Minute), at(105*time.Minute), 3)
	u2 := newClient(t)
	u2.login("u2")
	bid := bookAt(t, u2, "u2", slot)
	payAt(t, u2, bid)
	paySuccess(t, u2, bid)
	expectOK(t, u2, "/user/cancel", url.Values{"bookingId": {bid}, "reason": {"堵车可能迟到"}}, "客服审核")

	svc := newClient(t)
	svc.login("service")
	expectOK(t, svc, "/service/review", url.Values{"bookingId": {bid}, "decision": {"reject"}, "note": {"请按时到店"}}, "拒绝")
	expectErr(t, svc, "/service/review", url.Values{"bookingId": {bid}, "decision": {"reject"}}, "不允许")
	if st := bookingStatus(t, bid); st != model.BookingConfirmed {
		t.Fatalf("拒绝后应恢复已确认，got %s", st)
	}
	if refundIDOf(t, bid) != "" {
		t.Fatalf("拒绝取消不得发起退款")
	}
	rep := newClient(t)
	rep.login("reception")
	expectOK(t, rep, "/front/checkin", url.Values{"bookingId": {bid}}, "核销成功")
	expectErr(t, rep, "/front/checkin", url.Values{"bookingId": {bid}}, "不允许")

	// 服务流程：开始→完成，重复操作被拒且收入只记一次
	staff := newClient(t)
	staff.login("staff")
	expectOK(t, staff, "/staff/start", url.Values{"bookingId": {bid}}, "开始")
	expectErr(t, staff, "/staff/start", url.Values{"bookingId": {bid}}, "不允许")
	expectOK(t, staff, "/staff/finish", url.Values{"bookingId": {bid}}, "完成")
	expectErr(t, staff, "/staff/finish", url.Values{"bookingId": {bid}}, "不允许")
	var finishes int64
	mustQuery(t, &finishes, "SELECT COUNT(*) FROM service_records WHERE booking_id=? AND action='finish'", bid)
	if finishes != 1 {
		t.Fatalf("重复完成不得重复记收入，finish 记录=%d", finishes)
	}
	if st := bookingStatus(t, bid); st != model.BookingCompleted {
		t.Fatalf("服务完成应为 completed，got %s", st)
	}
}

func testServiceFlowTwice(t *testing.T) {
	slot := mgrSlot(t, at(2*time.Hour), at(3*time.Hour), 3)
	u3 := newClient(t)
	u3.login("u3")
	bid := bookAt(t, u3, "u3", slot)
	payAt(t, u3, bid)
	paySuccess(t, u3, bid)

	rep := newClient(t)
	rep.login("reception")
	expectOK(t, rep, "/front/checkin", url.Values{"bookingId": {bid}}, "核销成功")

	// 服务角色不得核销（403，非重定向）
	staff := newClient(t)
	staff.login("staff")
	for i := 1; i <= 2; i++ {
		if r := staff.postForm("/front/checkin", url.Values{"bookingId": {bid}}); r.StatusCode != 403 {
			t.Fatalf("round%d: 服务人员核销应 403，got %d", i, r.StatusCode)
		}
	}
	expectOK(t, staff, "/staff/start", url.Values{"bookingId": {bid}}, "开始")
	expectOK(t, staff, "/staff/finish", url.Values{"bookingId": {bid}}, "完成")
	if page := u3.getBody("/user"); !strings.Contains(rowHTML(page, bid), "已完成") {
		t.Fatalf("用户页应显示已完成")
	}
}

func testNoShowTwice(t *testing.T) {
	slot := mgrSlot(t, at(1*time.Minute), at(2*time.Minute), 3)
	u1 := newClient(t)
	u1.login("u1")
	bid := bookAt(t, u1, "u1", slot)
	payAt(t, u1, bid)
	paySuccess(t, u1, bid)

	clk.Advance(3 * time.Minute)
	defer clk.Advance(-3 * time.Minute)

	mgr := newClient(t)
	mgr.login("manager")
	expectOK(t, mgr, "/manager/noshow", url.Values{"bookingId": {bid}, "reason": {"主人未到店"}}, "未到店")
	expectErr(t, mgr, "/manager/noshow", url.Values{"bookingId": {bid}, "reason": {"再标记"}}, "不允许")
	if st := bookingStatus(t, bid); st != model.BookingNoShow {
		t.Fatalf("应标记未到店，got %s", st)
	}
	if refundIDOf(t, bid) != "" {
		t.Fatalf("未到店按题面约定不自动退款")
	}
	if page := u1.getBody("/user"); !strings.Contains(rowHTML(page, bid), "未到店") {
		t.Fatalf("用户页应显示未到店")
	}
}

func testCreateSlotTwice(t *testing.T) {
	mgr := newClient(t)
	mgr.login("manager")
	for i := 1; i <= 2; i++ {
		start := at(20*time.Hour + time.Duration(i)*time.Minute)
		end := start.Add(time.Hour)
		if r := mgr.postForm("/manager/slot", url.Values{
			"start": {localStr(start)}, "end": {localStr(end)}, "capacity": {"3"}}); !strings.Contains(r.Header.Get("Location"), "msg=") {
			t.Fatalf("round%d: 合法时段应创建成功: %s", i, r.Header.Get("Location"))
		}
		if r := mgr.postForm("/manager/slot", url.Values{
			"start": {localStr(end)}, "end": {localStr(start)}, "capacity": {"3"}}); !strings.Contains(r.Header.Get("Location"), "err=") {
			t.Fatalf("round%d: 结束早于开始应被拒绝", i)
		}
	}
}

func testRefundNoPaymentTwice(t *testing.T) {
	slot := mgrSlot(t, at(21*time.Hour), at(22*time.Hour), 3)
	u2 := newClient(t)
	u2.login("u2")
	bid := bookAt(t, u2, "u2", slot)
	for i := 1; i <= 2; i++ {
		if err := paySvc.InitRefund(t.Context(), bid); err == nil || err.Error() != model.ErrRefundNoPayment.Error() {
			t.Fatalf("round%d: 未收款退款应被拒绝，got %v", i, err)
		}
	}
	expectOK(t, u2, "/user/cancel", url.Values{"bookingId": {bid}}, "已取消")
}

func testSummaryTwice(t *testing.T) {
	var snapshots [][]bookingSummary
	for i := 1; i <= 2; i++ {
		rows, err := bookSvc.DailySummary(t.Context())
		if err != nil {
			t.Fatalf("round%d: %v", i, err)
		}
		var snap []bookingSummary
		for _, r := range rows {
			snap = append(snap, bookingSummary{r.Label, r.Count})
		}
		snapshots = append(snapshots, snap)
	}
	if len(snapshots[0]) != len(snapshots[1]) {
		t.Fatalf("两次汇总行数不一致")
	}
	for i := range snapshots[0] {
		if snapshots[0][i] != snapshots[1][i] {
			t.Fatalf("两次汇总应一致: %v vs %v", snapshots[0][i], snapshots[1][i])
		}
	}
	labels := map[string]bool{}
	for _, r := range snapshots[0] {
		labels[r.label] = true
	}
	for _, want := range []string{"已完成", "已取消", "未到店", "已收款合计", "已退款合计", "已完成服务笔数"} {
		if !labels[want] {
			t.Fatalf("汇总缺少口径: %s", want)
		}
	}
}

type bookingSummary struct {
	label string
	count int64
}

// testAftercareApproveTwice 服务完成后投诉 → 客服批准 → 全额退款（新业务）。
func testAftercareApproveTwice(t *testing.T) {
	slot := mgrSlot(t, at(22*time.Hour), at(23*time.Hour), 3)
	u1 := newClient(t)
	u1.login("u1")
	bid := bookAt(t, u1, "u1", slot)
	payAt(t, u1, bid)
	paySuccess(t, u1, bid)
	completeService(t, bid)

	if page := u1.getBody("/user"); !strings.Contains(rowHTML(page, bid), "申请售后") {
		t.Fatalf("已完成订单应展示售后入口")
	}
	// 未填原因被拒
	expectErr(t, u1, "/user/complaint", url.Values{"bookingId": {bid}, "reason": {"  "}}, "请填写售后原因")
	expectOK(t, u1, "/user/complaint", url.Values{"bookingId": {bid}, "reason": {"洗澡后皮肤过敏"}}, "售后申请已提交")
	expectErr(t, u1, "/user/complaint", url.Values{"bookingId": {bid}, "reason": {"重复提交"}}, "已提交过")
	if st := complaintStatusOf(t, bid); st != model.ComplaintPending {
		t.Fatalf("售后初始应待处理，got %s", st)
	}
	if page := u1.getBody("/user"); !strings.Contains(rowHTML(page, bid), "售后待处理") {
		t.Fatalf("用户页应显示售后待处理")
	}

	svc := newClient(t)
	svc.login("service")
	if page := svc.getBody("/service"); !strings.Contains(page, "售后服务待处理") || !strings.Contains(page, "洗澡后皮肤过敏") {
		t.Fatalf("客服页应展示售后申请")
	}
	cid := complaintIDOf(t, bid)
	expectOK(t, svc, "/service/complaint", url.Values{"complaintId": {cid}, "decision": {"approve"}, "note": {"核实属实"}}, "批准")
	expectErr(t, svc, "/service/complaint", url.Values{"complaintId": {cid}, "decision": {"approve"}}, "不允许")
	if st := complaintStatusOf(t, bid); st != model.ComplaintApproved {
		t.Fatalf("售后应已批准，got %s", st)
	}
	rid := refundIDOf(t, bid)
	if rid == "" {
		t.Fatalf("售后批准应生成退款单")
	}
	// 渠道成功 → 用户页显示已退款 + 售后已退款
	svc.postForm("/mock/refund", url.Values{"refundId": {rid}, "action": {"success"}})
	var rstatus string
	mustQuery(t, &rstatus, "SELECT status FROM refunds WHERE id=?", rid)
	if rstatus != model.RefundSuccess {
		t.Fatalf("退款应成功，got %s", rstatus)
	}
	row := rowHTML(u1.getBody("/user"), bid)
	if !strings.Contains(row, "已退款") || !strings.Contains(row, "售后已退款") {
		t.Fatalf("用户页应显示已退款与售后已退款")
	}
	// 同一预约不可重复退款：退款单唯一
	var n int64
	mustQuery(t, &n, "SELECT COUNT(*) FROM refunds WHERE booking_id=?", bid)
	if n != 1 {
		t.Fatalf("同一预约退款单必须唯一，got %d", n)
	}
}

// testAftercareRejectTwice 服务完成后投诉 → 客服驳回（记录原因，不退款）。
func testAftercareRejectTwice(t *testing.T) {
	slot := mgrSlot(t, at(23*time.Hour), at(24*time.Hour), 3)
	u2 := newClient(t)
	u2.login("u2")
	bid := bookAt(t, u2, "u2", slot)
	payAt(t, u2, bid)
	paySuccess(t, u2, bid)
	completeService(t, bid)

	expectOK(t, u2, "/user/complaint", url.Values{"bookingId": {bid}, "reason": {"对造型不满意"}}, "售后申请已提交")
	cid := complaintIDOf(t, bid)
	svc := newClient(t)
	svc.login("service")
	expectOK(t, svc, "/service/complaint", url.Values{"complaintId": {cid}, "decision": {"reject"}, "note": {"已完成造型确认，非质量问题"}}, "驳回")
	expectErr(t, svc, "/service/complaint", url.Values{"complaintId": {cid}, "decision": {"reject"}}, "不允许")
	if st := complaintStatusOf(t, bid); st != model.ComplaintRejected {
		t.Fatalf("售后应已驳回，got %s", st)
	}
	if refundIDOf(t, bid) != "" {
		t.Fatalf("驳回不得生成退款")
	}
	row := rowHTML(u2.getBody("/user"), bid)
	if !strings.Contains(row, "售后已驳回") || !strings.Contains(row, "非质量问题") {
		t.Fatalf("用户页应显示驳回结果与说明，got %s", row)
	}
}

// testAftercareNotCompletedTwice 未完成服务不可申请售后（含他人订单越权）。
func testAftercareNotCompletedTwice(t *testing.T) {
	slot := mgrSlot(t, at(25*time.Hour), at(26*time.Hour), 3)
	u3 := newClient(t)
	u3.login("u3")
	bid := bookAt(t, u3, "u3", slot)
	payAt(t, u3, bid)
	paySuccess(t, u3, bid)

	for i := 1; i <= 2; i++ {
		expectErr(t, u3, "/user/complaint", url.Values{"bookingId": {bid}, "reason": {"还没做就投诉"}}, "仅服务已完成")
		expectErr(t, newClientWith(t, "u1"), "/user/complaint", url.Values{"bookingId": {bid}, "reason": {"越权投诉"}}, "无权")
	}
	// 清理：取消该预约
	expectOK(t, u3, "/user/cancel", url.Values{"bookingId": {bid}}, "自动受理")
}

// testPagesTwice 页面级冒烟：六类页面与登录/登出/首页跳转（命令式渲染分支）。
func testPagesTwice(t *testing.T) {
	for i := 1; i <= 2; i++ {
		anon := newClient(t)
		if r := anon.get("/login"); r.StatusCode != 200 {
			t.Fatalf("round%d: /login=%d", i, r.StatusCode)
		}
		if r := anon.get("/"); r.StatusCode != 302 || !strings.Contains(r.Header.Get("Location"), "/login") {
			t.Fatalf("round%d: 未登录首页应跳登录", i)
		}
		if r := anon.get("/mock"); r.StatusCode != 200 {
			t.Fatalf("round%d: /mock=%d", i, r.StatusCode)
		}
		for _, role := range []string{"u1", "reception", "staff", "service", "manager"} {
			c := newClientWith(t, role)
			r := c.get("/")
			if r.StatusCode != 302 {
				t.Fatalf("round%d: %s 首页应跳转，got %d", i, role, r.StatusCode)
			}
			switch role {
			case "u1":
				if r := c.get("/user"); r.StatusCode != 200 {
					t.Fatalf("round%d: /user=%d", i, r.StatusCode)
				}
			case "reception", "staff":
				if r := c.get("/front"); r.StatusCode != 200 {
					t.Fatalf("round%d: /front=%d", i, r.StatusCode)
				}
			case "service":
				if r := c.get("/service"); r.StatusCode != 200 {
					t.Fatalf("round%d: /service=%d", i, r.StatusCode)
				}
			case "manager":
				if r := c.get("/manager"); r.StatusCode != 200 {
					t.Fatalf("round%d: /manager=%d", i, r.StatusCode)
				}
			}
			if r := c.postForm("/logout", nil); r.StatusCode != 302 {
				t.Fatalf("round%d: %s 登出失败", i, role)
			}
		}
	}
}

// testRefundNotifyTwice 渠道退款回调：无签名拒绝，合法签名生效且重复幂等。
func testRefundNotifyTwice(t *testing.T) {
	slot := mgrSlot(t, at(26*time.Hour), at(27*time.Hour), 3)
	u3 := newClient(t)
	u3.login("u3")
	bid := bookAt(t, u3, "u3", slot)
	payAt(t, u3, bid)
	paySuccess(t, u3, bid)
	expectOK(t, u3, "/user/cancel", url.Values{"bookingId": {bid}}, "自动受理")
	rid := refundIDOf(t, bid)
	if rid == "" {
		t.Fatalf("未生成退款单")
	}
	api := newClient(t)
	for i := 1; i <= 2; i++ {
		r := api.postForm("/api/refund-notify", url.Values{"refund_id": {rid}, "action": {"success"}, "sig": {"bad"}})
		if r.StatusCode != 400 {
			t.Fatalf("round%d: 无签名退款回调应 400，got %d", i, r.StatusCode)
		}
	}
	ok := api.postForm("/api/refund-notify", url.Values{
		"refund_id": {rid}, "action": {"success"}, "sig": {paySvc.SignRefund(rid, true)}})
	if ok.StatusCode != 200 {
		t.Fatalf("合法退款回调应 200，got %d", ok.StatusCode)
	}
	if st := refundStatusOf(t, bid); st != model.RefundSuccess {
		t.Fatalf("退款应成功，got %s", st)
	}
}

// testSweeperTwice 后台清扫协程可启动/停止（超时关闭由主流程覆盖）。
func testSweeperTwice(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	bookSvc.StartSweeper(ctx, 10*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)
}

// testSessionRedisTwice 生产会话存储可用（本机 Redis 未启动时跳过）。
func testSessionRedisTwice(t *testing.T) {
	store, err := session.NewRedis("127.0.0.1:6379", "", time.Minute)
	if err != nil {
		t.Skipf("Redis 不可用，跳过: %v", err)
	}
	tok := session.NewToken()
	if err := store.Set(tok, 42, time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	if uid, err := store.Get(tok); err != nil || uid != 42 {
		t.Fatalf("get=%d err=%v", uid, err)
	}
	if err := store.Del(tok); err != nil {
		t.Fatalf("del: %v", err)
	}
	if _, err := store.Get(tok); err == nil {
		t.Fatalf("删除后不应命中")
	}
}

// testChannelErrorPathsTwice 渠道与订单异常路径：未知支付单/未知退款单/失败回调/未知订单支付。
func testChannelErrorPathsTwice(t *testing.T) {
	mock := newClient(t)
	for i := 1; i <= 2; i++ {
		if r := mock.postForm("/mock/pay", url.Values{"paymentId": {"PUNKNOWN"}, "action": {"success"}}); !strings.Contains(r.Header.Get("Location"), "err=") {
			t.Fatalf("round%d: 未知支付单应报错", i)
		}
		if r := mock.postForm("/mock/refund", url.Values{"refundId": {"RUNKNOWN"}, "action": {"success"}}); !strings.Contains(r.Header.Get("Location"), "err=") {
			t.Fatalf("round%d: 未知退款单应报错", i)
		}
		if r := mock.postForm("/api/refund-notify", url.Values{"refund_id": {"RUNKNOWN"}, "action": {"fail"},
			"sig": {paySvc.SignRefund("RUNKNOWN", false)}}); r.StatusCode != 400 {
			t.Fatalf("round%d: 未知退款单回调应 400，got %d", i, r.StatusCode)
		}
		// 未知预约发起支付
		u1 := newClientWith(t, "u1")
		expectErr(t, u1, "/user/pay", url.Values{"bookingId": {"BUNKNOWN00000000000000"}}, "未知订单")
		// 渠道返回失败结果（支付失败，不改变预约状态）
		slot := mgrSlot(t, at(27*time.Hour+time.Duration(i)*time.Minute), at(28*time.Hour+time.Duration(i)*time.Minute), 3)
		bid := bookAt(t, newClientWith(t, "u3"), "u3", slot)
		pid := payAt(t, newClientWith(t, "u3"), bid)
		r := mock.postForm("/mock/pay", url.Values{"paymentId": {pid}, "action": {"fail"}})
		if !strings.Contains(r.Header.Get("Location"), "pay_failed") {
			t.Fatalf("round%d: 支付失败回调应返回 pay_failed: %s", i, r.Header.Get("Location"))
		}
		if st := bookingStatus(t, bid); st != model.BookingPendingPayment {
			t.Fatalf("round%d: 支付失败不得改变预约状态，got %s", i, st)
		}
	}
}

// ---------- 帮助 ----------

func at(d time.Duration) time.Time { return clk.Now().Add(d) }

func localStr(tm time.Time) string { return tm.In(time.Local).Format("2006-01-02T15:04") }

// mgrSlot 店长发布时段，返回新时段 ID。
func mgrSlot(t *testing.T, start, end time.Time, capacity int) int64 {
	t.Helper()
	m := newClient(t)
	m.login("manager")
	r := m.postForm("/manager/slot", url.Values{
		"start": {localStr(start)}, "end": {localStr(end)}, "capacity": {fmt.Sprint(capacity)}})
	if loc := r.Header.Get("Location"); !strings.Contains(loc, "msg=") {
		t.Fatalf("发布时段失败: %s", loc)
	}
	var id int64
	if err := db.Conn.QueryRow(&id, "SELECT MAX(id) FROM time_slots"); err != nil {
		t.Fatalf("slot id: %v", err)
	}
	return id
}

// bookAt 预约并返回预约号（预约失败直接失败）。
func bookAt(t *testing.T, c *client, username string, slotID int64) string {
	t.Helper()
	expectOK(t, c, "/user/book", url.Values{
		"petId": {fmt.Sprint(petIDOf(t, username))}, "slotId": {fmt.Sprint(slotID)}}, "预约成功")
	var id string
	// active=1 保证取到当前有效预约（取消后 active 置 NULL，可再次预约）
	if err := db.Conn.QueryRow(&id,
		"SELECT id FROM bookings WHERE slot_id=? AND pet_id=? AND active=1 LIMIT 1",
		slotID, petIDOf(t, username)); err != nil {
		t.Fatalf("book id: %v", err)
	}
	return id
}

// payAt 发起支付并返回支付单号。
func payAt(t *testing.T, c *client, bid string) string {
	t.Helper()
	expectOK(t, c, "/user/pay", url.Values{"bookingId": {bid}}, "支付单")
	var id string
	if err := db.Conn.QueryRow(&id,
		"SELECT id FROM payments WHERE booking_id=? ORDER BY created_at DESC LIMIT 1", bid); err != nil {
		t.Fatalf("payment id: %v", err)
	}
	return id
}

// paySuccess 通过模拟渠道完成支付。
func paySuccess(t *testing.T, c *client, bid string) {
	t.Helper()
	pid := payAt(t, c, bid)
	r := c.postForm("/mock/pay", url.Values{"paymentId": {pid}, "action": {"success"}})
	if loc := r.Header.Get("Location"); !strings.Contains(loc, "paid") {
		t.Fatalf("模拟支付未成功: %s", loc)
	}
}

// notify 构造带签名回调；sig 为空时按渠道密钥签名。
func notify(t *testing.T, txn, bid string, amount int64, result, sig string) (int, string) {
	t.Helper()
	if sig == "" {
		sig = signOf(txn, bid, amount, result)
	}
	return postPayNotifyRaw(t, newClient(t), txn, bid, amount, result, sig)
}

// completeService 核销→开始→完成。
func completeService(t *testing.T, bid string) {
	t.Helper()
	rep := newClient(t)
	rep.login("reception")
	expectOK(t, rep, "/front/checkin", url.Values{"bookingId": {bid}}, "核销成功")
	staff := newClient(t)
	staff.login("staff")
	expectOK(t, staff, "/staff/start", url.Values{"bookingId": {bid}}, "开始")
	expectOK(t, staff, "/staff/finish", url.Values{"bookingId": {bid}}, "完成")
}

func newClientWith(t *testing.T, username string) *client {
	t.Helper()
	c := newClient(t)
	c.login(username)
	return c
}

func expectOK(t *testing.T, c *client, path string, v url.Values, substr string) {
	t.Helper()
	r := c.postForm(path, v)
	if r.StatusCode != 302 {
		t.Fatalf("%s: expect 302 got %d", path, r.StatusCode)
	}
	if f := flashOf(r.Header.Get("Location")); !strings.Contains(f, substr) {
		t.Fatalf("%s: flash=%q 未包含 %q", path, f, substr)
	}
}

func expectErr(t *testing.T, c *client, path string, v url.Values, substr string) {
	t.Helper()
	r := c.postForm(path, v)
	if r.StatusCode != 302 {
		t.Fatalf("%s: expect 302 got %d", path, r.StatusCode)
	}
	loc := r.Header.Get("Location")
	if !strings.Contains(loc, "err=") {
		t.Fatalf("%s: 期望错误重定向，got %q", path, loc)
	}
	if f := flashOf(loc); !strings.Contains(f, substr) {
		t.Fatalf("%s: 错误=%q 未包含 %q", path, f, substr)
	}
}

func flashOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		if m := u.Query().Get("msg"); m != "" {
			return m
		}
		return u.Query().Get("err")
	}
	return raw
}

// rowHTML 截取包含 key 的第一行表格 HTML，用于按单号断言。
func rowHTML(page, key string) string {
	for _, row := range strings.Split(page, "<tr") {
		if strings.Contains(row, key) {
			return row
		}
	}
	return ""
}

func mustQuery(t *testing.T, dest any, q string, args ...any) {
	t.Helper()
	if err := db.Conn.QueryRow(dest, q, args...); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
}

func refundIDOf(t *testing.T, bid string) string {
	t.Helper()
	var id string
	if err := db.Conn.QueryRow(&id, "SELECT id FROM refunds WHERE booking_id=? LIMIT 1", bid); err != nil {
		return ""
	}
	return id
}

func refundStatusOf(t *testing.T, bid string) string {
	t.Helper()
	var st string
	if err := db.Conn.QueryRow(&st, "SELECT status FROM refunds WHERE booking_id=? LIMIT 1", bid); err != nil {
		return ""
	}
	return st
}

func complaintIDOf(t *testing.T, bid string) string {
	t.Helper()
	var id string
	if err := db.Conn.QueryRow(&id, "SELECT id FROM complaints WHERE booking_id=? LIMIT 1", bid); err != nil {
		return ""
	}
	return id
}

func complaintStatusOf(t *testing.T, bid string) string {
	t.Helper()
	var st string
	if err := db.Conn.QueryRow(&st, "SELECT status FROM complaints WHERE booking_id=? LIMIT 1", bid); err != nil {
		return ""
	}
	return st
}
