// Package model 定义题目02 的领域常量、实体与领域错误。
// 金额一律人民币分（int64），禁止浮点；时间一律 UTC。
package model

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

// AmountCents 预约款即本次服务全款：9900 分（题面预置）。
const AmountCents int64 = 9900

// 角色（预置账号）。
const (
	RoleManager   = "manager"
	RoleUser      = "user"
	RoleReception = "reception"
	RoleStaff     = "staff"
	RoleService   = "service"
)

// 预约状态（状态机唯一事实来源，见 docs/01-需求分析.md §3）。
const (
	BookingPendingPayment = "pending_payment" // 待付款，占用名额
	BookingConfirmed      = "confirmed"       // 已确认（已收款）
	BookingCancelReview   = "cancel_review"   // 取消待审核（保持名额，不得核销）
	BookingCancelled      = "cancelled"       // 已取消（名额已释放）
	BookingCheckedIn      = "checked_in"      // 已核销
	BookingInService      = "in_service"      // 服务中
	BookingCompleted      = "completed"       // 已完成
	BookingNoShow         = "no_show"         // 未到店（店长标记，不自动退款）
	BookingClosed         = "closed"          // 待付款超时关闭
	BookingPaymentAnomaly = "payment_anomaly" // 异常支付（关闭后收到成功通知）
)

// ActiveStatuses 占用名额/构成有效预约的状态（bookings.active=1）。
var ActiveStatuses = []string{
	BookingPendingPayment, BookingConfirmed, BookingCancelReview,
	BookingCheckedIn, BookingInService, BookingCompleted,
}

// 支付/退款状态（值全局唯一，便于展示层做统一标签映射）。
const (
	PayInitiated = "initiated"
	PayPaid      = "paid"
	PayFailed    = "pay_failed"

	RefundPending = "pending"
	RefundSuccess = "refund_success"
	RefundFailed  = "refund_failed"
)

// 售后服务状态（服务完成后用户可投诉/申请售后，客服核实后退款或驳回）。
const (
	ComplaintPending  = "aftercare_pending"  // 待客服处理
	ComplaintApproved = "aftercare_approved" // 已批准（原路退款）
	ComplaintRejected = "aftercare_rejected" // 已驳回
)

// 领域错误（HTTP 层映射为可读提示并记录）。
var (
	ErrNotFound        = errors.New("记录不存在")
	ErrSlotNotFound    = errors.New("时段不存在")
	ErrSlotFull        = errors.New("时段名额不足")
	ErrSlotStarted     = errors.New("时段已开始，不可预约")
	ErrSlotClosed      = errors.New("时段未开放预约")
	ErrDupBooking      = errors.New("同一宠物同一时段已有有效预约")
	ErrForbidden       = errors.New("无权操作该记录")
	ErrInvalidState    = errors.New("当前状态不允许该操作")
	ErrReasonRequired  = errors.New("临近开始取消须填写原因")
	ErrReviewPending   = errors.New("已存在待审核的取消申请")
	ErrBadSignature    = errors.New("回调验签失败")
	ErrUnknownOrder    = errors.New("未知订单")
	ErrAmountMismatch  = errors.New("支付金额与订单不符")
	ErrTxnConflict     = errors.New("渠道交易号已归属其他订单")
	ErrUnknownPet      = errors.New("宠物不存在")
	ErrRefundNoPayment = errors.New("该预约无已收款记录，无需退款")

	ErrComplaintReasonRequired = errors.New("请填写售后原因")
	ErrComplaintNotCompleted   = errors.New("仅服务已完成的预约可申请售后")
	ErrComplaintExists         = errors.New("该预约已提交过售后申请，请等待客服处理")
	ErrComplaintNotFound       = errors.New("售后申请不存在")
)

// User 预置账号。
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string
	DisplayName  string
}

// Pet 宠物（预置，不做档案编辑）。
type Pet struct {
	ID      int64
	OwnerID int64
	Name    string
}

// Slot 可预约时段。
type Slot struct {
	ID       int64
	StartAt  time.Time
	EndAt    time.Time
	Capacity int
	Status   string
}

// Booking 预约单。Active=1 表示有效占用态；终态由业务层置 NULL，
// 扫描时以 COALESCE(active,0) 归零。空文本列以 COALESCE(col,”) 扫描。
type Booking struct {
	ID             string
	SlotID         int64
	UserID         int64
	PetID          int64
	Status         string
	CancelReason   string
	ReviewDecision string
	ReviewNote     string
	NoShowReason   string
	Active         int64
	CreatedAt      time.Time
	PaidAt         time.Time
	PayDeadline    time.Time
}

// Payment 支付单。
type Payment struct {
	ID         string
	BookingID  string
	Amount     int64
	Status     string
	ChannelTxn string
	CreatedAt  time.Time
	PaidAt     time.Time
}

// Refund 退款单（一笔支付最多一条，重试累加 attempt）。
type Refund struct {
	ID         string
	BookingID  string
	PaymentID  string
	Amount     int64
	Status     string
	FailReason string
	Attempt    int
	CreatedAt  time.Time
	DoneAt     time.Time
}

// ServiceRecord 核销/开始/完成记录，唯一(booking_id, action) 保证不重复记收入。
type ServiceRecord struct {
	ID         int64
	BookingID  string
	Action     string
	OperatorID int64
	CreatedAt  time.Time
}

// NewID 生成业务单号：前缀 + UTC 时间戳 + 随机后缀。
func NewID(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return prefix + time.Now().UTC().Format("20060102150405") + hex.EncodeToString(b)
}
