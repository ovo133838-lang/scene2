// Package aftercare 售后服务：服务完成后用户可就「宠物状态/服务不满意」投诉，
// 客服核实后批准（原路全额退款）或驳回，退款状态复用支付域，独立可查。
package aftercare

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

// Refunder 由支付域实现：售后批准后按原支付单发起全额退款。
type Refunder interface {
	InitRefund(ctx context.Context, bookingID string) error
}

// Service 售后服务。
type Service struct {
	Conn     sqlx.SqlConn
	Clock    clock.Clock
	Refunder Refunder // 可为 nil（仅记录，不退款）
}

func (s *Service) now() time.Time { return s.Clock.Now() }

// View 售后单视图（客服工作台与用户查询）。
type View struct {
	ID           string
	BookingID    string
	Username     string
	PetName      string
	Reason       string
	Status       string
	DecisionNote string
	RefundID     string
	RefundStatus string
	Amount       int64
	CreatedAt    time.Time
}

// Submit 用户对已完成服务的预约发起售后申请，每单仅一次。
func (s *Service) Submit(ctx context.Context, userID int64, bookingID, reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", model.ErrComplaintReasonRequired
	}
	id := model.NewID("C")
	err := s.Conn.Transact(func(session sqlx.Session) error {
		var b struct {
			ID     string
			UserID int64
			Status string
		}
		if err := session.QueryRowCtx(ctx, &b,
			"SELECT id,user_id,status FROM bookings WHERE id=? FOR UPDATE", bookingID); err != nil {
			if errors.Is(err, sqlx.ErrNotFound) {
				return model.ErrNotFound
			}
			return err
		}
		if b.UserID != userID {
			return model.ErrForbidden
		}
		if b.Status != model.BookingCompleted {
			return model.ErrComplaintNotCompleted
		}
		var exists int64
		if err := session.QueryRowCtx(ctx, &exists,
			"SELECT COUNT(*) FROM complaints WHERE booking_id=?", bookingID); err != nil {
			return err
		}
		if exists > 0 {
			return model.ErrComplaintExists
		}
		_, err := session.ExecCtx(ctx,
			"INSERT INTO complaints(id,booking_id,user_id,reason,status,created_at) VALUES(?,?,?,?,?,?)",
			id, bookingID, userID, reason, model.ComplaintPending, s.now())
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// Review 客服处理售后申请：批准则发起原路退款，驳回则记录原因。
func (s *Service) Review(ctx context.Context, complaintID string, approve bool, note string) error {
	var bookingID string
	err := s.Conn.Transact(func(session sqlx.Session) error {
		var c struct {
			ID        string
			BookingID string
			Status    string
		}
		if err := session.QueryRowCtx(ctx, &c,
			"SELECT id,booking_id,status FROM complaints WHERE id=? FOR UPDATE", complaintID); err != nil {
			if errors.Is(err, sqlx.ErrNotFound) {
				return model.ErrComplaintNotFound
			}
			return err
		}
		if c.Status != model.ComplaintPending {
			return model.ErrInvalidState
		}
		bookingID = c.BookingID
		status := model.ComplaintRejected
		if approve {
			status = model.ComplaintApproved
		}
		_, err := session.ExecCtx(ctx,
			"UPDATE complaints SET status=?, decision_note=?, handled_at=? WHERE id=?",
			status, strings.TrimSpace(note), s.now(), complaintID)
		return err
	})
	if err != nil {
		return err
	}
	if approve && s.Refunder != nil {
		if rerr := s.Refunder.InitRefund(ctx, bookingID); rerr != nil {
			return fmt.Errorf("售后已批准，退款发起失败（可在退款失败列表重试）: %w", rerr)
		}
	}
	return nil
}

const complaintViewSQL = "SELECT c.id, c.booking_id, u.username, p.name AS pet_name, c.reason, c.status, " +
	"COALESCE(c.decision_note,'') AS decision_note, COALESCE(r.id,'') AS refund_id, " +
	"COALESCE(r.status,'') AS refund_status, COALESCE(pay.amount,0) AS amount, c.created_at " +
	"FROM complaints c " +
	"JOIN bookings b ON b.id=c.booking_id " +
	"JOIN users u ON u.id=c.user_id " +
	"JOIN pets p ON p.id=b.pet_id " +
	"LEFT JOIN payments pay ON pay.booking_id=b.id AND pay.status='paid' " +
	"LEFT JOIN refunds r ON r.booking_id=b.id "

// ListByStatus 按状态列出售后单（客服工作台）。
func (s *Service) ListByStatus(ctx context.Context, status string) ([]View, error) {
	var vs []View
	err := s.Conn.QueryRowsCtx(ctx, &vs, complaintViewSQL+
		"WHERE c.status=? ORDER BY c.created_at", status)
	return vs, err
}

// ListUserComplaints 用户本人的售后单（按创建时间倒序）。
func (s *Service) ListUserComplaints(ctx context.Context, userID int64) ([]View, error) {
	var vs []View
	err := s.Conn.QueryRowsCtx(ctx, &vs, complaintViewSQL+
		"WHERE c.user_id=? ORDER BY c.created_at DESC", userID)
	return vs, err
}
