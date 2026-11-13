// Package payment 实现题目02 支付域：发起支付、渠道通知（验签/幂等/金额匹配/
// 迟到支付异常）、退款发起与失败重试。
package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/zeromicro/go-zero/core/stores/sqlx"

	"petshop/internal/clock"
	"petshop/internal/model"
)

type Service struct {
	Conn   sqlx.SqlConn
	Clock  clock.Clock
	Secret string // 渠道回调 HMAC-SHA256 密钥（环境注入，不入库不入仓库）
}

// Notify 渠道回调内容。
type Notify struct {
	ChannelTxn string    // 渠道交易号（全局唯一）
	BookingID  string    // 商城订单号（预约单号）
	Amount     int64     // 单位：分
	Result     string    // success / fail
	EventTime  time.Time // 渠道事件时间
}

// 签名：HMAC-SHA256(secret, "txn|booking|amount|result|eventUnix")
func (s *Service) Sign(n Notify) string {
	mac := hmac.New(sha256.New, []byte(s.Secret))
	fmt.Fprintf(mac, "%s|%s|%d|%s|%d", n.ChannelTxn, n.BookingID, n.Amount, n.Result, n.EventTime.Unix())
	return hex.EncodeToString(mac.Sum(nil))
}

// SignRefund 退款结果回调签名：HMAC-SHA256(secret, "refund|refundID|success")。
func (s *Service) SignRefund(refundID string, success bool) string {
	mac := hmac.New(sha256.New, []byte(s.Secret))
	fmt.Fprintf(mac, "refund|%s|%t", refundID, success)
	return hex.EncodeToString(mac.Sum(nil))
}

// ---------- 发起支付 ----------

// InitiatePayment 用户为待付款预约发起支付（幂等：已有未支付支付单则复用）。
func (s *Service) InitiatePayment(ctx context.Context, userID int64, bookingID string) (*model.Payment, error) {
	now := s.Clock.Now()
	var out *model.Payment
	err := s.Conn.Transact(func(session sqlx.Session) error {
		var b model.Booking
		if err := session.QueryRowCtx(ctx, &b,
			"SELECT id,slot_id,user_id,pet_id,status,"+
				"COALESCE(cancel_reason,'') AS cancel_reason,"+
				"COALESCE(review_decision,'') AS review_decision,"+
				"COALESCE(review_note,'') AS review_note,"+
				"COALESCE(no_show_reason,'') AS no_show_reason,"+
				"COALESCE(active,0) AS active,created_at,"+
				"COALESCE(paid_at,created_at) AS paid_at,pay_deadline "+
				"FROM bookings WHERE id=? FOR UPDATE", bookingID); err != nil {
			if errors.Is(err, sqlx.ErrNotFound) {
				return model.ErrUnknownOrder
			}
			return err
		}
		if b.UserID != userID {
			return model.ErrForbidden
		}
		if b.Status != model.BookingPendingPayment {
			return model.ErrInvalidState
		}
		var p model.Payment
		err := session.QueryRowCtx(ctx, &p,
			"SELECT id,booking_id,amount,status,COALESCE(channel_txn,'') AS channel_txn,created_at,"+
				"COALESCE(paid_at,created_at) AS paid_at FROM payments WHERE booking_id=? ORDER BY id DESC LIMIT 1",
			bookingID)
		if err == nil {
			out = &p
			return nil // 复用既有支付单
		}
		if !errors.Is(err, sqlx.ErrNotFound) {
			return err
		}
		np := &model.Payment{
			ID: model.NewID("P"), BookingID: bookingID,
			Amount: model.AmountCents, Status: model.PayInitiated, CreatedAt: now,
		}
		if _, err := session.ExecCtx(ctx,
			"INSERT INTO payments(id,booking_id,amount,status,created_at) VALUES(?,?,?,?,?)",
			np.ID, np.BookingID, np.Amount, np.Status, np.CreatedAt); err != nil {
			return err
		}
		out = np
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- 渠道通知 ----------

// HandleNotify 处理支付回调：
//   - 验签失败/未知订单/金额不符 → 记录并可查询，返回对应领域错误；
//   - 重复通知 → 幂等返回成功（不重复记款）；
//   - 同一渠道交易号归属第二个订单 → 拒绝并记录；
//   - 预约待付款且在有效期内 → 确认收款，预约进入已确认；
//   - 预约已超时关闭 → 不恢复预约与名额，登记异常支付并自动发起退款；
//   - 预约已取消（回调竞态） → 保持取消，自动退款。
func (s *Service) HandleNotify(ctx context.Context, n Notify, sig string) (handled string, err error) {
	now := s.Clock.Now()
	sigOK := s.Sign(n) == sig

	// 全部回调留痕（含验签失败）
	defer func() {
		if _, e := s.Conn.ExecCtx(ctx,
			"INSERT INTO payment_notifications(channel_txn,booking_id,amount,sig_ok,result,handled,received_at) "+
				"VALUES(?,?,?,?,?,?,?)",
			n.ChannelTxn, n.BookingID, n.Amount, boolToInt(sigOK), n.Result, handled, now); e != nil && err == nil {
			err = e
		}
	}()

	if !sigOK {
		handled = "sig_invalid"
		return handled, model.ErrBadSignature
	}
	var b model.Booking
	if e := s.Conn.QueryRowCtx(ctx, &b,
		"SELECT id,slot_id,user_id,pet_id,status,"+
			"COALESCE(cancel_reason,'') AS cancel_reason,"+
			"COALESCE(review_decision,'') AS review_decision,"+
			"COALESCE(review_note,'') AS review_note,"+
			"COALESCE(no_show_reason,'') AS no_show_reason,"+
			"COALESCE(active,0) AS active,created_at,"+
			"COALESCE(paid_at,created_at) AS paid_at,pay_deadline "+
			"FROM bookings WHERE id=?", n.BookingID); e != nil {
		if errors.Is(e, sqlx.ErrNotFound) {
			handled = "unknown_order"
			return handled, model.ErrUnknownOrder
		}
		return handled, e
	}
	var p model.Payment
	if e := s.Conn.QueryRowCtx(ctx, &p,
		"SELECT id,booking_id,amount,status,COALESCE(channel_txn,'') AS channel_txn,created_at,"+
			"COALESCE(paid_at,created_at) AS paid_at FROM payments WHERE booking_id=? ORDER BY id DESC LIMIT 1",
		n.BookingID); e != nil {
		if errors.Is(e, sqlx.ErrNotFound) {
			handled = "unknown_order"
			return handled, model.ErrUnknownOrder
		}
		return handled, e
	}
	if n.Amount != p.Amount || n.Amount != model.AmountCents {
		handled = "amount_mismatch"
		return handled, model.ErrAmountMismatch
	}
	if p.Status == model.PayPaid {
		handled = "duplicate"
		return handled, nil // 重复通知不重复记款
	}
	var conflict int64
	if e := s.Conn.QueryRowCtx(ctx, &conflict,
		"SELECT COUNT(*) FROM payments WHERE channel_txn=? AND id<>?",
		n.ChannelTxn, p.ID); e != nil {
		return handled, e
	}
	if conflict > 0 {
		handled = "txn_conflict"
		return handled, model.ErrTxnConflict
	}

	if n.Result != "success" {
		if _, e := s.Conn.ExecCtx(ctx,
			"UPDATE payments SET status=? WHERE id=?", model.PayFailed, p.ID); e != nil {
			return handled, e
		}
		handled = "pay_failed"
		return handled, nil
	}

	err = s.Conn.Transact(func(session sqlx.Session) error {
		var st string
		if e := session.QueryRowCtx(ctx, &st,
			"SELECT status FROM bookings WHERE id=? FOR UPDATE", n.BookingID); e != nil {
			return e
		}
		switch st {
		case model.BookingPendingPayment:
			if _, e := session.ExecCtx(ctx,
				"UPDATE payments SET status=?, channel_txn=?, paid_at=? WHERE id=?",
				model.PayPaid, n.ChannelTxn, now, p.ID); e != nil {
				return e
			}
			_, e := session.ExecCtx(ctx,
				"UPDATE bookings SET status=?, paid_at=? WHERE id=? AND status=?",
				model.BookingConfirmed, now, n.BookingID, model.BookingPendingPayment)
			if e == nil {
				handled = "paid"
			}
			return e
		case model.BookingClosed:
			// 迟到成功：不恢复预约与名额，登记异常支付并退款
			if _, e := session.ExecCtx(ctx,
				"UPDATE payments SET status=?, channel_txn=?, paid_at=? WHERE id=?",
				model.PayPaid, n.ChannelTxn, now, p.ID); e != nil {
				return e
			}
			if _, e := session.ExecCtx(ctx,
				"UPDATE bookings SET status=? WHERE id=?", model.BookingPaymentAnomaly, n.BookingID); e != nil {
				return e
			}
			handled = "late_anomaly"
			return nil
		case model.BookingCancelled:
			if _, e := session.ExecCtx(ctx,
				"UPDATE payments SET status=?, channel_txn=?, paid_at=? WHERE id=?",
				model.PayPaid, n.ChannelTxn, now, p.ID); e != nil {
				return e
			}
			handled = "late_cancelled"
			return nil
		default:
			handled = "state_conflict:" + st
			return fmt.Errorf("%w: 预约状态 %s 不接受支付确认", model.ErrInvalidState, st)
		}
	})
	if err != nil {
		return handled, err
	}
	if handled == "late_anomaly" || handled == "late_cancelled" {
		// 已收款但业务已终结 → 自动原路退款（状态独立可查，失败由客服跟进）
		if e := s.InitRefund(ctx, n.BookingID); e != nil {
			return handled, fmt.Errorf("自动退款发起失败（待客服跟进）: %w", e)
		}
	}
	return handled, nil
}

// ---------- 退款 ----------

// InitRefund 为已收款预约创建退款单（一笔支付一条，幂等）。
func (s *Service) InitRefund(ctx context.Context, bookingID string) error {
	now := s.Clock.Now()
	return s.Conn.Transact(func(session sqlx.Session) error {
		var p model.Payment
		if e := session.QueryRowCtx(ctx, &p,
			"SELECT id,booking_id,amount,status,COALESCE(channel_txn,'') AS channel_txn,"+
				"created_at,COALESCE(paid_at,created_at) AS paid_at "+
				"FROM payments WHERE booking_id=? AND status=? ORDER BY id DESC LIMIT 1",
			bookingID, model.PayPaid); e != nil {
			if errors.Is(e, sqlx.ErrNotFound) {
				return model.ErrRefundNoPayment
			}
			return e
		}
		var exists int64
		if e := session.QueryRowCtx(ctx, &exists,
			"SELECT COUNT(*) FROM refunds WHERE payment_id=?", p.ID); e != nil {
			return e
		}
		if exists > 0 {
			return nil // 已有退款单（含成功/失败），不重复创建
		}
		_, e := session.ExecCtx(ctx,
			"INSERT INTO refunds(id,booking_id,payment_id,amount,status,attempt,created_at) "+
				"VALUES(?,?,?,?,?,1,?)",
			model.NewID("R"), bookingID, p.ID, p.Amount, model.RefundPending, now)
		return e
	})
}

// HandleRefundResult 渠道退款回调：成功终结退款；明确失败保留待客服重试。
// 重复通知幂等（已终态不重复处理）。
func (s *Service) HandleRefundResult(ctx context.Context, refundID string, success bool, reason string) (string, error) {
	now := s.Clock.Now()
	handled := ""
	err := s.Conn.Transact(func(session sqlx.Session) error {
		var r model.Refund
		if e := session.QueryRowCtx(ctx, &r,
			"SELECT id,booking_id,payment_id,amount,status,"+
				"COALESCE(fail_reason,'') AS fail_reason,attempt,created_at,"+
				"COALESCE(done_at,created_at) AS done_at FROM refunds WHERE id=? FOR UPDATE",
			refundID); e != nil {
			if errors.Is(e, sqlx.ErrNotFound) {
				return model.ErrNotFound
			}
			return e
		}
		if r.Status != model.RefundPending {
			handled = "duplicate"
			return nil
		}
		if success {
			if _, e := session.ExecCtx(ctx,
				"UPDATE refunds SET status=?, done_at=? WHERE id=?",
				model.RefundSuccess, now, refundID); e != nil {
				return e
			}
			handled = "refund_success"
			return nil
		}
		if _, e := session.ExecCtx(ctx,
			"UPDATE refunds SET status=?, fail_reason=?, attempt=attempt+1 WHERE id=?",
			model.RefundFailed, reason, refundID); e != nil {
			return e
		}
		handled = "refund_failed"
		return nil
	})
	return handled, err
}

// RetryRefund 客服重试失败退款：回到待处理并累加尝试次数。
func (s *Service) RetryRefund(ctx context.Context, refundID string) error {
	return s.Conn.Transact(func(session sqlx.Session) error {
		var r model.Refund
		if e := session.QueryRowCtx(ctx, &r,
			"SELECT id,booking_id,payment_id,amount,status,"+
				"COALESCE(fail_reason,'') AS fail_reason,attempt,created_at,"+
				"COALESCE(done_at,created_at) AS done_at FROM refunds WHERE id=? FOR UPDATE", refundID); e != nil {
			if errors.Is(e, sqlx.ErrNotFound) {
				return model.ErrNotFound
			}
			return e
		}
		if r.Status != model.RefundFailed {
			return model.ErrInvalidState
		}
		_, e := session.ExecCtx(ctx,
			"UPDATE refunds SET status=?, fail_reason=NULL, attempt=attempt+1 WHERE id=?",
			model.RefundPending, refundID)
		return e
	})
}

// RefundView 退款视图（客服跟进与预约关联查询）。
type RefundView struct {
	ID            string
	BookingID     string
	Amount        int64
	Status        string
	FailReason    string
	Attempt       int
	Username      string
	BookingStatus string
}

// ListRefunds 列出退款单（可按状态过滤）。
func (s *Service) ListRefunds(ctx context.Context, status string) ([]RefundView, error) {
	q := "SELECT r.id, r.booking_id, r.amount, r.status, COALESCE(r.fail_reason,'') AS fail_reason, " +
		"r.attempt, u.username, b.status AS booking_status " +
		"FROM refunds r JOIN bookings b ON b.id=r.booking_id JOIN users u ON u.id=b.user_id"
	var args []any
	if status != "" {
		q += " WHERE r.status=?"
		args = append(args, status)
	}
	q += " ORDER BY r.created_at"
	var vs []RefundView
	err := s.Conn.QueryRowsCtx(ctx, &vs, q, args...)
	return vs, err
}

// ListAnomalies 异常支付列表（店长/客服核查入口）。
func (s *Service) ListAnomalies(ctx context.Context) ([]model.Booking, error) {
	var bs []model.Booking
	err := s.Conn.QueryRowsCtx(ctx, &bs,
		"SELECT id,slot_id,user_id,pet_id,status,"+
			"COALESCE(cancel_reason,'') AS cancel_reason,"+
			"COALESCE(review_decision,'') AS review_decision,"+
			"COALESCE(review_note,'') AS review_note,"+
			"COALESCE(no_show_reason,'') AS no_show_reason,"+
			"COALESCE(active,0) AS active,created_at,"+
			"COALESCE(paid_at,created_at) AS paid_at,pay_deadline "+
			"FROM bookings WHERE status=? ORDER BY paid_at",
		model.BookingPaymentAnomaly)
	return bs, err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---------- 模拟渠道支撑 ----------

// MockPayment 模拟渠道控制台的支付单视图。
type MockPayment struct {
	ID            string
	BookingID     string
	Amount        int64
	Status        string
	ChannelTxn    string
	BookingStatus string
}

// ListMockPayments 列出支付单及其预约状态（演示迟到支付场景）。
func (s *Service) ListMockPayments(ctx context.Context) ([]MockPayment, error) {
	var vs []MockPayment
	err := s.Conn.QueryRowsCtx(ctx, &vs,
		"SELECT p.id, p.booking_id, p.amount, p.status, COALESCE(p.channel_txn,'') AS channel_txn, "+
			"b.status AS booking_status "+
			"FROM payments p JOIN bookings b ON b.id=p.booking_id "+
			"WHERE p.status IN('initiated','paid') ORDER BY p.created_at DESC LIMIT 50")
	return vs, err
}

// MockRefund 模拟渠道控制台的退款单视图。
type MockRefund struct {
	ID        string
	BookingID string
	Amount    int64
	Status    string
	Attempt   int
}

// ListMockRefunds 列出待处理退款单（演示成功/失败回调）。
func (s *Service) ListMockRefunds(ctx context.Context) ([]MockRefund, error) {
	var vs []MockRefund
	err := s.Conn.QueryRowsCtx(ctx, &vs,
		"SELECT id, booking_id, amount, status, attempt FROM refunds WHERE status=? ORDER BY created_at",
		model.RefundPending)
	return vs, err
}

// BuildNotify 由支付单构造渠道通知（模拟渠道使用；txn 为空则生成新交易号）。
func (s *Service) BuildNotify(ctx context.Context, payID, txn string) (*Notify, error) {
	var p model.Payment
	if err := s.Conn.QueryRowCtx(ctx, &p,
		"SELECT id,booking_id,amount,status,COALESCE(channel_txn,'') AS channel_txn,created_at,"+
			"COALESCE(paid_at,created_at) AS paid_at FROM payments WHERE id=?", payID); err != nil {
		if errors.Is(err, sqlx.ErrNotFound) {
			return nil, model.ErrNotFound
		}
		return nil, err
	}
	if txn == "" && p.ChannelTxn != "" {
		txn = p.ChannelTxn
	}
	if txn == "" {
		txn = model.NewID("TXN")
	}
	return &Notify{
		ChannelTxn: txn,
		BookingID:  p.BookingID,
		Amount:     p.Amount,
		Result:     "success",
		EventTime:  s.Clock.Now(),
	}, nil
}
