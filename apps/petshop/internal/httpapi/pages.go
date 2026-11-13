package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"petshop/internal/aftercare"
	"petshop/internal/booking"
	"petshop/internal/model"
	"petshop/internal/payment"
	"petshop/internal/session"
	"petshop/internal/store"
)

// ---------- 登录 / 首页 ----------

func (s *Server) pageLogin(w http.ResponseWriter, r *http.Request, _ *authUser) {
	s.render(w, "login.html", map[string]any{"Msg": r.FormValue("msg"), "Err": r.FormValue("err")})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")
	u, err := s.DB.GetUser(r.Context(), username)
	if err != nil || u.PasswordHash != store.HashPassword(password) {
		redirectMsg(w, r, "/login", "", "用户名或口令错误")
		return
	}
	token := session.NewToken()
	if err := s.Sess.Set(token, u.ID, 12*time.Hour); err != nil {
		http.Error(w, "会话存储不可用: "+err.Error(), 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		_ = s.Sess.Del(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	switch u.Role {
	case model.RoleManager:
		http.Redirect(w, r, "/manager", http.StatusFound)
	case model.RoleUser:
		http.Redirect(w, r, "/user", http.StatusFound)
	case model.RoleReception, model.RoleStaff:
		http.Redirect(w, r, "/front", http.StatusFound)
	case model.RoleService:
		http.Redirect(w, r, "/service", http.StatusFound)
	default:
		http.Error(w, "未知角色", 500)
	}
}

// handleHealth 健康检查（部署流水线使用）。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.HealthCheck(r.Context()); err != nil {
		http.Error(w, "db down", 503)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// ---------- 用户工作台 ----------

type userPageData struct {
	Auth       *authUser
	Msg, Err   string
	PayTTLMin  string
	Slots      []booking.SlotView
	Bookings   []booking.BookingView
	Aftercares []aftercare.View
}

func (s *Server) pageUser(w http.ResponseWriter, r *http.Request, u *authUser) {
	slots, err := s.Booking.ListOpenSlots(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	bookings, err := s.Booking.ListUserBookings(r.Context(), u.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	complaints, err := s.Aftercare.ListUserComplaints(r.Context(), u.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "user.html", &userPageData{
		Auth: u, Msg: r.FormValue("msg"), Err: r.FormValue("err"),
		PayTTLMin: fmt.Sprintf("%.0f", s.Booking.PayTTL.Minutes()),
		Slots:     slots, Bookings: bookings, Aftercares: complaints,
	})
}

func (s *Server) handleBook(w http.ResponseWriter, r *http.Request, u *authUser) {
	id, err := s.Booking.CreateBooking(r.Context(), u.ID,
		parseInt64(r.FormValue("petId")), parseInt64(r.FormValue("slotId")))
	if err != nil {
		redirectMsg(w, r, "/user", "", errMessage(err))
		return
	}
	redirectMsg(w, r, "/user", "预约成功，单号 "+id+"，请在付款期限内完成支付", "")
}

func (s *Server) handlePay(w http.ResponseWriter, r *http.Request, u *authUser) {
	p, err := s.Pay.InitiatePayment(r.Context(), u.ID, r.FormValue("bookingId"))
	if err != nil {
		redirectMsg(w, r, "/user", "", errMessage(err))
		return
	}
	// 跳转模拟渠道控制台完成/模拟支付结果（真实渠道为外部跳转）
	redirectMsg(w, r, "/mock", "已创建支付单 "+p.ID+"，请在模拟渠道操作支付结果", "")
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request, u *authUser) {
	out, err := s.Booking.CancelByUser(r.Context(), u.ID, r.FormValue("bookingId"), r.FormValue("reason"))
	if err != nil {
		redirectMsg(w, r, "/user", "", errMessage(err))
		return
	}
	msg := ""
	switch {
	case out.AutoAccepted:
		msg = "已自动受理取消，名额已释放，退款处理中"
	case out.ToReview:
		msg = "临近开始取消已提交客服审核，审核期间名额保留且不可核销"
	default:
		msg = "预约已取消，名额已释放"
	}
	redirectMsg(w, r, "/user", msg, "")
}

// handleComplaint 用户对已完成服务发起售后（投诉/不满意，申请原路退款）。
func (s *Server) handleComplaint(w http.ResponseWriter, r *http.Request, u *authUser) {
	id, err := s.Aftercare.Submit(r.Context(), u.ID,
		r.FormValue("bookingId"), r.FormValue("reason"))
	if err != nil {
		redirectMsg(w, r, "/user", "", errMessage(err))
		return
	}
	redirectMsg(w, r, "/user", "售后申请已提交（单号 "+id+"），客服将在核实后处理", "")
}

// ---------- 前台 / 服务人员工作台 ----------

type frontPageData struct {
	Auth      *authUser
	Msg, Err  string
	Confirmed []booking.BookingView
	CheckedIn []booking.BookingView
	InService []booking.BookingView
}

func (s *Server) pageFront(w http.ResponseWriter, r *http.Request, u *authUser) {
	confirmed, err := s.Booking.ListByStatus(r.Context(), []string{model.BookingConfirmed})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	checkedIn, err := s.Booking.ListByStatus(r.Context(), []string{model.BookingCheckedIn})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	inService, err := s.Booking.ListByStatus(r.Context(), []string{model.BookingInService})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "front.html", &frontPageData{
		Auth: u, Msg: r.FormValue("msg"), Err: r.FormValue("err"),
		Confirmed: confirmed, CheckedIn: checkedIn, InService: inService,
	})
}

func (s *Server) handleCheckin(w http.ResponseWriter, r *http.Request, u *authUser) {
	if err := s.Booking.Checkin(r.Context(), u.ID, r.FormValue("bookingId")); err != nil {
		redirectMsg(w, r, "/front", "", errMessage(err))
		return
	}
	redirectMsg(w, r, "/front", "核销成功，等待服务人员登记开始", "")
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request, u *authUser) {
	if err := s.Booking.StartService(r.Context(), u.ID, r.FormValue("bookingId")); err != nil {
		redirectMsg(w, r, "/front", "", errMessage(err))
		return
	}
	redirectMsg(w, r, "/front", "服务已登记开始", "")
}

func (s *Server) handleFinish(w http.ResponseWriter, r *http.Request, u *authUser) {
	if err := s.Booking.FinishService(r.Context(), u.ID, r.FormValue("bookingId")); err != nil {
		redirectMsg(w, r, "/front", "", errMessage(err))
		return
	}
	redirectMsg(w, r, "/front", "服务已登记完成（收入确认一次）", "")
}

// ---------- 客服工作台 ----------

type servicePageData struct {
	Auth          *authUser
	Msg, Err      string
	Reviews       []booking.BookingView
	RefundFail    []payment.RefundView
	RefundPending []payment.RefundView
	Anomalies     []model.Booking
	Aftercare     []aftercare.View
	AftercareDone []aftercare.View
}

func (s *Server) pageService(w http.ResponseWriter, r *http.Request, u *authUser) {
	reviews, err := s.Booking.ListByStatus(r.Context(), []string{model.BookingCancelReview})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	refundFail, err := s.Pay.ListRefunds(r.Context(), model.RefundFailed)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	refundPending, err := s.Pay.ListRefunds(r.Context(), model.RefundPending)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	anomalies, err := s.Pay.ListAnomalies(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	aftercarePending, err := s.Aftercare.ListByStatus(r.Context(), model.ComplaintPending)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	aftercareApproved, err := s.Aftercare.ListByStatus(r.Context(), model.ComplaintApproved)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	aftercareRejected, err := s.Aftercare.ListByStatus(r.Context(), model.ComplaintRejected)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "service.html", &servicePageData{
		Auth: u, Msg: r.FormValue("msg"), Err: r.FormValue("err"),
		Reviews: reviews, RefundFail: refundFail, RefundPending: refundPending, Anomalies: anomalies,
		Aftercare: aftercarePending, AftercareDone: append(aftercareApproved, aftercareRejected...),
	})
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request, _ *authUser) {
	approve := r.FormValue("decision") == "approve"
	if err := s.Booking.ReviewCancel(r.Context(), 0, r.FormValue("bookingId"), approve, r.FormValue("note")); err != nil {
		redirectMsg(w, r, "/service", "", errMessage(err))
		return
	}
	if approve {
		redirectMsg(w, r, "/service", "已批准取消，名额释放，退款处理中", "")
		return
	}
	redirectMsg(w, r, "/service", "已拒绝取消，预约恢复为已确认", "")
}

func (s *Server) handleRefundRetry(w http.ResponseWriter, r *http.Request, _ *authUser) {
	if err := s.Pay.RetryRefund(r.Context(), r.FormValue("refundId")); err != nil {
		redirectMsg(w, r, "/service", "", errMessage(err))
		return
	}
	redirectMsg(w, r, "/service", "退款已重新发起，等待渠道结果", "")
}

// handleComplaintReview 客服处理售后：批准则原路退款，驳回则记录原因。
func (s *Server) handleComplaintReview(w http.ResponseWriter, r *http.Request, _ *authUser) {
	approve := r.FormValue("decision") == "approve"
	if err := s.Aftercare.Review(r.Context(), r.FormValue("complaintId"), approve, r.FormValue("note")); err != nil {
		redirectMsg(w, r, "/service", "", errMessage(err))
		return
	}
	if approve {
		redirectMsg(w, r, "/service", "售后已批准，全额退款处理中", "")
		return
	}
	redirectMsg(w, r, "/service", "售后已驳回，已记录处理说明", "")
}

// ---------- 店长工作台 ----------

type managerPageData struct {
	Auth     *authUser
	Msg, Err string
	Summary  []booking.SummaryRow
	NoShow   []booking.BookingView
}

func (s *Server) pageManager(w http.ResponseWriter, r *http.Request, u *authUser) {
	summary, err := s.Booking.DailySummary(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	noShow, err := s.Booking.ListNoShowCandidates(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "manager.html", &managerPageData{
		Auth: u, Msg: r.FormValue("msg"), Err: r.FormValue("err"),
		Summary: summary, NoShow: noShow,
	})
}

func (s *Server) handleCreateSlot(w http.ResponseWriter, r *http.Request, _ *authUser) {
	start, err1 := time.ParseInLocation("2006-01-02T15:04", r.FormValue("start"), time.Local)
	end, err2 := time.ParseInLocation("2006-01-02T15:04", r.FormValue("end"), time.Local)
	if err1 != nil || err2 != nil {
		redirectMsg(w, r, "/manager", "", "时间格式错误")
		return
	}
	if _, err := s.Booking.CreateSlot(r.Context(), start.UTC(), end.UTC(),
		int(parseInt64(r.FormValue("capacity")))); err != nil {
		redirectMsg(w, r, "/manager", "", errMessage(err))
		return
	}
	redirectMsg(w, r, "/manager", "时段已发布", "")
}

func (s *Server) handleNoShow(w http.ResponseWriter, r *http.Request, _ *authUser) {
	if err := s.Booking.MarkNoShow(r.Context(), r.FormValue("bookingId"), r.FormValue("reason")); err != nil {
		redirectMsg(w, r, "/manager", "", errMessage(err))
		return
	}
	redirectMsg(w, r, "/manager", "已标记未到店（按题面约定不自动退款）", "")
}

// render 渲染模板。
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.Tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "render: "+err.Error(), 500)
	}
}
