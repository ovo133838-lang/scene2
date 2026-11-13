// Package booking 实现题目02 预约域核心业务：占位、取消、审核、核销、
// 服务进度、未到店、待付款超时关闭与店长日清汇总。
// 状态迁移只允许经由本包的事务方法完成（AGENTS.md §3）。
package booking

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/stores/sqlx"

	"petshop/internal/clock"
	"petshop/internal/model"
)

// Refunder 由支付域实现：取消获准后自动发起退款（原路）。
type Refunder interface {
	InitRefund(ctx context.Context, bookingID string) error
}

type Service struct {
	Conn         sqlx.SqlConn
	Clock        clock.Clock
	PayTTL       time.Duration // 未付款占位时长（默认 5 分钟）
	CancelWindow time.Duration // 提前取消线（默认 2 小时）
	Refunder     Refunder      // 可为 nil（测试个别路径）
}

func (s *Service) now() time.Time { return s.Clock.Now() }

// ---------- 时段 ----------

// CreateSlot 店长创建时段（校验起止时间）。
func (s *Service) CreateSlot(ctx context.Context, start, end time.Time, capacity int) (int64, error) {
	if !start.Before(end) {
		return 0, errors.New("时段开始时间必须早于结束时间")
	}
	if capacity <= 0 {
		return 0, errors.New("时段容量必须为正数")
	}
	r, err := s.Conn.ExecCtx(ctx,
		"INSERT INTO time_slots(start_at,end_at,capacity,status,created_at) VALUES(?,?,?,'open',?)",
		start, end, capacity, s.now())
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// SlotView 时段及其占用情况。
type SlotView struct {
	ID       int64
	StartAt  time.Time
	EndAt    time.Time
	Capacity int
	Taken    int64
}

// ListOpenSlots 列出开放且未结束的时段及已占名额。
func (s *Service) ListOpenSlots(ctx context.Context) ([]SlotView, error) {
	var vs []SlotView
	err := s.Conn.QueryRowsCtx(ctx, &vs,
		"SELECT s.id, s.start_at, s.end_at, s.capacity, "+
			"(SELECT COUNT(*) FROM slot_taken t WHERE t.slot_id = s.id) AS taken "+
			"FROM time_slots s WHERE s.status='open' AND s.end_at > ? ORDER BY s.start_at",
		s.now())
	return vs, err
}

// ---------- 预约创建 ----------

// CreateBooking 用户为宠物预约时段：占名额并生成待付款预约。
// 事务内 FOR UPDATE 锁时段行 + 占用计数 + (slot,pet,active) 唯一约束三重防超卖。
func (s *Service) CreateBooking(ctx context.Context, userID, petID, slotID int64) (string, error) {
	now := s.now()
	deadline := now.Add(s.PayTTL)
	id := model.NewID("B")

	err := s.Conn.Transact(func(session sqlx.Session) error {
		var slot model.Slot
		if err := session.QueryRowCtx(ctx, &slot,
			"SELECT id,start_at,end_at,capacity,status FROM time_slots WHERE id=? FOR UPDATE", slotID); err != nil {
			if errors.Is(err, sqlx.ErrNotFound) {
				return model.ErrSlotNotFound
			}
			return err
		}
		if slot.Status != "open" {
			return model.ErrSlotClosed
		}
		if !now.Before(slot.StartAt) {
			return model.ErrSlotStarted
		}
		var ownerID int64
		if err := session.QueryRowCtx(ctx, &ownerID,
			"SELECT owner_id FROM pets WHERE id=?", petID); err != nil {
			if errors.Is(err, sqlx.ErrNotFound) {
				return model.ErrUnknownPet
			}
			return err
		}
		if ownerID != userID {
			return model.ErrForbidden
		}
		var dup int64
		if err := session.QueryRowCtx(ctx, &dup,
			"SELECT COUNT(*) FROM bookings WHERE slot_id=? AND pet_id=? AND active=1",
			slotID, petID); err != nil {
			return err
		}
		if dup > 0 {
			return model.ErrDupBooking
		}
		var taken int64
		if err := session.QueryRowCtx(ctx, &taken,
			"SELECT COUNT(*) FROM slot_taken WHERE slot_id=?", slotID); err != nil {
			return err
		}
		if taken >= int64(slot.Capacity) {
			return model.ErrSlotFull
		}
		if _, err := session.ExecCtx(ctx,
			"INSERT INTO bookings(id,slot_id,user_id,pet_id,status,active,created_at,pay_deadline) "+
				"VALUES(?,?,?,?,?,1,?,?)",
			id, slotID, userID, petID, model.BookingPendingPayment, now, deadline); err != nil {
			return err
		}
		_, err := session.ExecCtx(ctx,
			"INSERT INTO slot_taken(booking_id,slot_id) VALUES(?,?)", id, slotID)
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// ---------- 取消与审核 ----------

// CancelOutcome 取消结果。
type CancelOutcome struct {
	Released     bool // 名额是否本次释放
	RefundNeeded bool // 是否需要退款（已收款）
	ToReview     bool // 是否进入客服审核
	AutoAccepted bool // 是否满足提前取消线自动受理
}

// CancelByUser 用户取消：
//   - 待付款 → 直接取消并释放；
//   - 已确认且距开始 ≥ CancelWindow → 自动受理，释放并自动退款；
//   - 已确认且距开始 < CancelWindow → 须填原因，进入客服审核（保持名额，不得核销）。
func (s *Service) CancelByUser(ctx context.Context, userID int64, bookingID, reason string) (*CancelOutcome, error) {
	now := s.now()
	reason = strings.TrimSpace(reason)
	out := &CancelOutcome{}

	err := s.Conn.Transact(func(session sqlx.Session) error {
		b, slot, err := loadBookingForUpdate(ctx, session, bookingID)
		if err != nil {
			return err
		}
		if b.UserID != userID {
			return model.ErrForbidden
		}
		switch b.Status {
		case model.BookingPendingPayment:
			out.Released = true
			return release(ctx, session, b.ID, model.BookingCancelled)
		case model.BookingConfirmed:
			if slot.StartAt.Sub(now) >= s.CancelWindow {
				out.Released, out.RefundNeeded, out.AutoAccepted = true, true, true
				return release(ctx, session, b.ID, model.BookingCancelled)
			}
			if reason == "" {
				return model.ErrReasonRequired
			}
			out.ToReview = true
			_, err := session.ExecCtx(ctx,
				"UPDATE bookings SET status=?, cancel_reason=? WHERE id=?",
				model.BookingCancelReview, reason, b.ID)
			return err
		case model.BookingCancelReview:
			return model.ErrReviewPending
		default:
			return model.ErrInvalidState
		}
	})
	if err != nil {
		return nil, err
	}
	// 事务提交后发起退款；失败仅记录，退款单可由客服跟进重试（名额已释放不受影响）
	if out.RefundNeeded && s.Refunder != nil {
		if rerr := s.Refunder.InitRefund(ctx, bookingID); rerr != nil {
			return out, fmt.Errorf("退款发起失败（已登记待跟进）: %w", rerr)
		}
	}
	return out, nil
}

// ReviewCancel 客服审批临近开始的取消申请。
func (s *Service) ReviewCancel(ctx context.Context, serviceID int64, bookingID string, approve bool, note string) error {
	err := s.Conn.Transact(func(session sqlx.Session) error {
		b, _, err := loadBookingForUpdate(ctx, session, bookingID)
		if err != nil {
			return err
		}
		if b.Status != model.BookingCancelReview {
			return model.ErrInvalidState
		}
		decision := "rejected"
		if approve {
			decision = "approved"
		}
		if _, err := session.ExecCtx(ctx,
			"UPDATE bookings SET review_decision=?, review_note=? WHERE id=?",
			decision, note, b.ID); err != nil {
			return err
		}
		if approve {
			_, err = session.ExecCtx(ctx,
				"UPDATE bookings SET status=?, active=NULL WHERE id=?",
				model.BookingCancelled, b.ID)
			if err != nil {
				return err
			}
			_, err = session.ExecCtx(ctx, "DELETE FROM slot_taken WHERE booking_id=?", b.ID)
			return err
		}
		// 拒绝：预约恢复为已确认，名额保留，可继续核销
		_, err = session.ExecCtx(ctx,
			"UPDATE bookings SET status=? WHERE id=?", model.BookingConfirmed, b.ID)
		return err
	})
	if err != nil {
		return err
	}
	if approve && s.Refunder != nil {
		if rerr := s.Refunder.InitRefund(ctx, bookingID); rerr != nil {
			return fmt.Errorf("退款发起失败（已登记待跟进）: %w", rerr)
		}
	}
	return nil
}

// ---------- 核销与服务 ----------

func (s *Service) Checkin(ctx context.Context, operatorID int64, bookingID string) error {
	return s.serviceAction(ctx, operatorID, bookingID, model.BookingConfirmed, model.BookingCheckedIn, "checkin", false)
}

func (s *Service) StartService(ctx context.Context, operatorID int64, bookingID string) error {
	return s.serviceAction(ctx, operatorID, bookingID, model.BookingCheckedIn, model.BookingInService, "start", false)
}

func (s *Service) FinishService(ctx context.Context, operatorID int64, bookingID string) error {
	return s.serviceAction(ctx, operatorID, bookingID, model.BookingInService, model.BookingCompleted, "finish", true)
}

// serviceAction 通用服务进度流转：条件更新 + service_records 唯一键防重复。
// income=true 的动作（完成）即收入确认点，重复完成被唯一键拒绝。
func (s *Service) serviceAction(ctx context.Context, operatorID int64, bookingID, from, to, action string, _ bool) error {
	now := s.now()
	return s.Conn.Transact(func(session sqlx.Session) error {
		b, slot, err := loadBookingForUpdate(ctx, session, bookingID)
		if err != nil {
			return err
		}
		if b.Status != from {
			return model.ErrInvalidState
		}
		// 核销前置：本店员工（单店场景由路由层校验角色）+ 已确认 + 无待审取消；
		// 核销须在时段结束前完成。
		if action == "checkin" && !now.Before(slot.EndAt) {
			return model.ErrInvalidState
		}
		if _, err := session.ExecCtx(ctx,
			"INSERT INTO service_records(booking_id,action,operator_id,created_at) VALUES(?,?,?,?)",
			bookingID, action, operatorID, now); err != nil {
			// 唯一键冲突 → 重复核销/开始/完成
			return fmt.Errorf("%w: %s 重复操作", model.ErrInvalidState, action)
		}
		_, err = session.ExecCtx(ctx, "UPDATE bookings SET status=? WHERE id=?", to, bookingID)
		return err
	})
}

// ---------- 未到店 ----------

// MarkNoShow 店长在时段结束后标记未到店并写明原因（题面：不自动退款）。
func (s *Service) MarkNoShow(ctx context.Context, bookingID, reason string) error {
	now := s.now()
	if reason == "" {
		return errors.New("须填写未到店原因")
	}
	return s.Conn.Transact(func(session sqlx.Session) error {
		b, slot, err := loadBookingForUpdate(ctx, session, bookingID)
		if err != nil {
			return err
		}
		if b.Status != model.BookingConfirmed {
			return model.ErrInvalidState
		}
		if now.Before(slot.EndAt) {
			return errors.New("时段尚未结束，不能标记未到店")
		}
		if _, err := session.ExecCtx(ctx,
			"UPDATE bookings SET status=?, no_show_reason=?, active=NULL WHERE id=?",
			model.BookingNoShow, reason, b.ID); err != nil {
			return err
		}
		_, err = session.ExecCtx(ctx, "DELETE FROM slot_taken WHERE booking_id=?", b.ID)
		return err
	})
}

// ---------- 超时关闭 ----------

// CloseExpired 关闭全部过期待付款预约，返回本次关闭数量。
// 幂等：slot_taken 删除与状态条件更新保证重复执行不多释放。
func (s *Service) CloseExpired(ctx context.Context) (int, error) {
	now := s.now()
	var ids []string
	if err := s.Conn.QueryRowsCtx(ctx, &ids,
		"SELECT id FROM bookings WHERE status=? AND pay_deadline < ?",
		model.BookingPendingPayment, now); err != nil {
		return 0, err
	}
	closed := 0
	for _, id := range ids {
		err := s.Conn.Transact(func(session sqlx.Session) error {
			var status string
			if err := session.QueryRowCtx(ctx, &status,
				"SELECT status FROM bookings WHERE id=? FOR UPDATE", id); err != nil {
				return err
			}
			if status != model.BookingPendingPayment {
				return nil // 已被并发处理，不重复关闭
			}
			if err := release(ctx, session, id, model.BookingClosed); err != nil {
				return err
			}
			closed++
			return nil
		})
		if err != nil {
			return closed, err
		}
	}
	return closed, nil
}

// StartSweeper 启动后台超时清扫（教学时钟可注入；演示可缩短时长）。
func (s *Service) StartSweeper(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_, _ = s.CloseExpired(ctx)
			}
		}
	}()
}

// ---------- 查询 ----------

// BookingView 列表/详情视图。
type BookingView struct {
	ID           string
	SlotID       int64
	SlotStart    time.Time
	SlotEnd      time.Time
	UserID       int64
	Username     string
	PetName      string
	Status       string
	Amount       int64
	PayStatus    string
	RefundID     string
	RefundStatus string
	RefundFail   string
	CreatedAt    time.Time
	CancelReason string

	// 售后服务（服务完成后可投诉/申请退款）
	ComplaintID     string
	ComplaintStatus string
	ComplaintReason string
	ComplaintNote   string
}

const bookingViewSQL = "SELECT b.id, b.slot_id, s.start_at AS slot_start, s.end_at AS slot_end, " +
	"b.user_id, u.username, p.name AS pet_name, b.status, " +
	"COALESCE(pay.amount,0) AS amount, COALESCE(pay.status,'') AS pay_status, " +
	"COALESCE(r.id,'') AS refund_id, COALESCE(r.status,'') AS refund_status, " +
	"COALESCE(r.fail_reason,'') AS refund_fail, " +
	"b.created_at, COALESCE(b.cancel_reason,'') AS cancel_reason, " +
	"COALESCE(c.id,'') AS complaint_id, COALESCE(c.status,'') AS complaint_status, " +
	"COALESCE(c.reason,'') AS complaint_reason, COALESCE(c.decision_note,'') AS complaint_note " +
	"FROM bookings b " +
	"JOIN time_slots s ON s.id=b.slot_id " +
	"JOIN users u ON u.id=b.user_id " +
	"JOIN pets p ON p.id=b.pet_id " +
	"LEFT JOIN payments pay ON pay.booking_id=b.id AND pay.status IN('paid','initiated') " +
	"LEFT JOIN refunds r ON r.booking_id=b.id " +
	"LEFT JOIN complaints c ON c.booking_id=b.id "

// ListUserBookings 用户本人的预约与订单（含退款状态，独立可查）。
func (s *Service) ListUserBookings(ctx context.Context, userID int64) ([]BookingView, error) {
	var vs []BookingView
	err := s.Conn.QueryRowsCtx(ctx, &vs, bookingViewSQL+
		"WHERE b.user_id=? ORDER BY b.created_at DESC", userID)
	return vs, err
}

// ListByStatus 按状态列出预约（前台/客服/店长工作台）。
func (s *Service) ListByStatus(ctx context.Context, statuses []string) ([]BookingView, error) {
	q := bookingViewSQL + "WHERE b.status IN(" + placeholders(len(statuses)) + ")" +
		" ORDER BY s.start_at, b.created_at"
	args := make([]any, 0, len(statuses))
	for _, st := range statuses {
		args = append(args, st)
	}
	var vs []BookingView
	err := s.Conn.QueryRowsCtx(ctx, &vs, q, args...)
	return vs, err
}

// ListNoShowCandidates 时段已结束且已确认（未到店）的预约，供店长标记。
func (s *Service) ListNoShowCandidates(ctx context.Context) ([]BookingView, error) {
	var vs []BookingView
	err := s.Conn.QueryRowsCtx(ctx, &vs, bookingViewSQL+
		"WHERE b.status=? AND s.end_at <= ? ORDER BY s.end_at",
		model.BookingConfirmed, s.now())
	return vs, err
}

// SummaryRow 日清汇总行。
type SummaryRow struct {
	Label string
	Count int64
	Note  string
}

// DailySummary 店长收店核查：分类计数 + 金额口径（收款/退款/未退款）。
// 口径：已收款=支付成功流水合计；已完成服务笔数=finish 记录数；
// 已付款不等于已完成（未到店/退款/异常支付分别列示）。
func (s *Service) DailySummary(ctx context.Context) ([]SummaryRow, error) {
	now := s.now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayEnd := dayStart.Add(24 * time.Hour)

	rows := []SummaryRow{}
	statuses := []struct{ key, label string }{
		{model.BookingPendingPayment, "待付款"},
		{model.BookingConfirmed, "已确认"},
		{model.BookingCancelReview, "取消待审核"},
		{model.BookingCancelled, "已取消"},
		{model.BookingCheckedIn, "已核销"},
		{model.BookingInService, "服务中"},
		{model.BookingCompleted, "已完成"},
		{model.BookingNoShow, "未到店"},
		{model.BookingClosed, "超时关闭"},
		{model.BookingPaymentAnomaly, "异常支付"},
	}
	for _, st := range statuses {
		var n int64
		if err := s.Conn.QueryRowCtx(ctx, &n,
			"SELECT COUNT(*) FROM bookings b JOIN time_slots s ON s.id=b.slot_id "+
				"WHERE b.status=? AND s.start_at>=? AND s.start_at<?", st.key, dayStart, dayEnd); err != nil {
			return nil, err
		}
		rows = append(rows, SummaryRow{Label: st.label, Count: n})
	}

	var paid, refunded int64
	if err := s.Conn.QueryRowCtx(ctx, &paid,
		"SELECT COALESCE(SUM(amount),0) FROM payments WHERE status=? AND paid_at>=? AND paid_at<?",
		model.PayPaid, dayStart, dayEnd); err != nil {
		return nil, err
	}
	if err := s.Conn.QueryRowCtx(ctx, &refunded,
		"SELECT COALESCE(SUM(r.amount),0) FROM refunds r WHERE r.status=? AND r.done_at>=? AND r.done_at<?",
		model.RefundSuccess, dayStart, dayEnd); err != nil {
		return nil, err
	}
	var pendingRefund, finishCount int64
	if err := s.Conn.QueryRowCtx(ctx, &pendingRefund,
		"SELECT COUNT(*) FROM refunds WHERE status<>? AND created_at>=? AND created_at<?",
		model.RefundSuccess, dayStart, dayEnd); err != nil {
		return nil, err
	}
	if err := s.Conn.QueryRowCtx(ctx, &finishCount,
		"SELECT COUNT(*) FROM service_records r "+
			"JOIN bookings b ON b.id=r.booking_id JOIN time_slots s ON s.id=b.slot_id "+
			"WHERE r.action='finish' AND s.start_at>=? AND s.start_at<?",
		dayStart, dayEnd); err != nil {
		return nil, err
	}
	rows = append(rows,
		SummaryRow{Label: "已收款合计", Count: paid, Note: "分"},
		SummaryRow{Label: "已退款合计", Count: refunded, Note: "分"},
		SummaryRow{Label: "退款未完成笔数", Count: pendingRefund, Note: "含失败待跟进"},
		SummaryRow{Label: "已完成服务笔数", Count: finishCount, Note: "收入确认依据"},
	)
	return rows, nil
}

// ---------- 内部工具 ----------

func loadBookingForUpdate(ctx context.Context, session sqlx.Session, id string) (*model.Booking, *model.Slot, error) {
	var b model.Booking
	if err := session.QueryRowCtx(ctx, &b,
		"SELECT id,slot_id,user_id,pet_id,status,"+
			"COALESCE(cancel_reason,'') AS cancel_reason,"+
			"COALESCE(review_decision,'') AS review_decision,"+
			"COALESCE(review_note,'') AS review_note,"+
			"COALESCE(no_show_reason,'') AS no_show_reason,"+
			"COALESCE(active,0) AS active,created_at,"+
			"COALESCE(paid_at,created_at) AS paid_at,pay_deadline "+
			"FROM bookings WHERE id=? FOR UPDATE", id); err != nil {
		if errors.Is(err, sqlx.ErrNotFound) {
			return nil, nil, model.ErrNotFound
		}
		return nil, nil, err
	}
	var slot model.Slot
	if err := session.QueryRowCtx(ctx, &slot,
		"SELECT id,start_at,end_at,capacity,status FROM time_slots WHERE id=?", b.SlotID); err != nil {
		return nil, nil, err
	}
	return &b, &slot, nil
}

// release 终态化预约并释放名额（slot_taken 删除幂等，重复调用不多释放）。
func release(ctx context.Context, session sqlx.Session, bookingID, toStatus string) error {
	if _, err := session.ExecCtx(ctx,
		"UPDATE bookings SET status=?, active=NULL WHERE id=?", toStatus, bookingID); err != nil {
		return err
	}
	_, err := session.ExecCtx(ctx, "DELETE FROM slot_taken WHERE booking_id=?", bookingID)
	return err
}

func placeholders(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			s += ","
		}
		s += "?"
	}
	return s
}
