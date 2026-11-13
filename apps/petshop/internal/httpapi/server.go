// Package httpapi 提供题目02 的页面与接口：登录会话、按角色鉴权、
// 用户/前台/客服/店长工作台、渠道回调接口与模拟渠道控制台。
// 身份一律取自会话，不信任请求参数（防止越权）。
package httpapi

import (
	"embed"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"petshop/internal/aftercare"
	"petshop/internal/booking"
	"petshop/internal/model"
	"petshop/internal/payment"
	"petshop/internal/session"
	"petshop/internal/store"
)

//go:embed templates/*.html
var tmplFS embed.FS

// Server HTTP 服务。
type Server struct {
	DB        *store.DB
	Booking   *booking.Service
	Pay       *payment.Service
	Aftercare *aftercare.Service
	Sess      session.Store
	Tmpl      *template.Template
	mux       *http.ServeMux
}

// New 装配路由与模板。
func New(db *store.DB, b *booking.Service, p *payment.Service, a *aftercare.Service, sess session.Store) (*Server, error) {
	funcs := template.FuncMap{
		"fmtTime": func(t time.Time) string { return t.Local().Format("01-02 15:04") },
		"statusLabel": func(st string) string {
			m := map[string]string{
				model.BookingPendingPayment: "待付款", model.BookingConfirmed: "已确认",
				model.BookingCancelReview: "取消待审核", model.BookingCancelled: "已取消",
				model.BookingCheckedIn: "已核销", model.BookingInService: "服务中",
				model.BookingCompleted: "已完成", model.BookingNoShow: "未到店",
				model.BookingClosed: "超时关闭", model.BookingPaymentAnomaly: "异常支付",
				model.PayInitiated: "待支付", model.PayPaid: "已支付", model.PayFailed: "支付失败",
				model.RefundPending: "退款中", model.RefundSuccess: "已退款", model.RefundFailed: "退款失败",
				model.ComplaintPending: "售后待处理", model.ComplaintApproved: "售后已退款",
				model.ComplaintRejected: "售后已驳回",
			}
			if v, ok := m[st]; ok {
				return v
			}
			return st
		},
	}
	tmpl, err := template.New("").Funcs(funcs).ParseFS(tmplFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{DB: db, Booking: b, Pay: p, Aftercare: a, Sess: sess, Tmpl: tmpl}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /health", s.handleHealth)

	m.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) { s.pageLogin(w, r, nil) })
	m.HandleFunc("POST /login", s.handleLogin)
	m.HandleFunc("POST /logout", s.handleLogout)
	m.HandleFunc("GET /{$}", s.handleHome)

	m.HandleFunc("GET /user", s.requireRole(s.pageUser, model.RoleUser))
	m.HandleFunc("POST /user/book", s.requireRole(s.handleBook, model.RoleUser))
	m.HandleFunc("POST /user/pay", s.requireRole(s.handlePay, model.RoleUser))
	m.HandleFunc("POST /user/cancel", s.requireRole(s.handleCancel, model.RoleUser))
	m.HandleFunc("POST /user/complaint", s.requireRole(s.handleComplaint, model.RoleUser))

	m.HandleFunc("GET /front", s.requireRole(s.pageFront, model.RoleReception, model.RoleStaff))
	m.HandleFunc("POST /front/checkin", s.requireRole(s.handleCheckin, model.RoleReception))
	m.HandleFunc("POST /staff/start", s.requireRole(s.handleStart, model.RoleStaff))
	m.HandleFunc("POST /staff/finish", s.requireRole(s.handleFinish, model.RoleStaff))

	m.HandleFunc("GET /service", s.requireRole(s.pageService, model.RoleService))
	m.HandleFunc("POST /service/review", s.requireRole(s.handleReview, model.RoleService))
	m.HandleFunc("POST /service/refund-retry", s.requireRole(s.handleRefundRetry, model.RoleService))
	m.HandleFunc("POST /service/complaint", s.requireRole(s.handleComplaintReview, model.RoleService))

	m.HandleFunc("GET /manager", s.requireRole(s.pageManager, model.RoleManager))
	m.HandleFunc("POST /manager/slot", s.requireRole(s.handleCreateSlot, model.RoleManager))
	m.HandleFunc("POST /manager/noshow", s.requireRole(s.handleNoShow, model.RoleManager))

	// 渠道回调（真实 HTTP 接口，curl/外部模拟均可触达，须验签）
	m.HandleFunc("POST /api/notify", s.handleNotify)
	m.HandleFunc("POST /api/refund-notify", s.handleRefundNotify)
	// 模拟渠道控制台（演示用，构造带签名回调调用同一处理入口）
	m.HandleFunc("GET /mock", s.pageMock)
	m.HandleFunc("POST /mock/pay", s.handleMockPay)
	m.HandleFunc("POST /mock/refund", s.handleMockRefund)

	s.mux = m
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// ---------- 会话与鉴权 ----------

const cookieName = "petshop_session"

type authUser struct {
	ID   int64
	Role string
	Name string
	Pets []model.Pet
}

// currentUser 从会话恢复身份，失败返回 nil。
func (s *Server) currentUser(r *http.Request) *authUser {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	uid, err := s.Sess.Get(c.Value)
	if err != nil {
		return nil
	}
	var row struct {
		ID   int64
		Role string
		Name string
	}
	err = s.DB.Conn.QueryRowCtx(r.Context(), &row,
		"SELECT id, role, display_name AS name FROM users WHERE id=?", uid)
	if err != nil {
		return nil
	}
	return &authUser{ID: row.ID, Role: row.Role, Name: row.Name}
}

// roleHandler 需要身份的处理器。
type roleHandler func(w http.ResponseWriter, r *http.Request, u *authUser)

// requireRole 会话校验 + 角色白名单。
func (s *Server) requireRole(next roleHandler, roles ...string) http.HandlerFunc {
	allow := map[string]bool{}
	for _, r := range roles {
		allow[r] = true
	}
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if !allow[u.Role] {
			http.Error(w, "403 无本页权限（角色鉴权）", http.StatusForbidden)
			return
		}
		if u.Role == model.RoleUser {
			_ = s.DB.Conn.QueryRowsCtx(r.Context(), &u.Pets,
				"SELECT id,owner_id,name FROM pets WHERE owner_id=?", u.ID)
		}
		next(w, r, u)
	}
}

// ---------- 工具 ----------

// errMessage 将领域错误转为可读提示（含包装错误链的根因）。
func errMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func redirectMsg(w http.ResponseWriter, r *http.Request, to, msg, errMsg string) {
	q := ""
	if msg != "" {
		q = "?msg=" + msg
	}
	if errMsg != "" {
		if q == "" {
			q = "?"
		} else {
			q += "&"
		}
		q += "err=" + errMsg
	}
	http.Redirect(w, r, to+q, http.StatusFound)
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
