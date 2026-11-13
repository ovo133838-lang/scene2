package httpapi

import (
	"crypto/subtle"
	"net/http"
	"time"

	"petshop/internal/payment"
)

// 模拟支付渠道：页面控制台 + 渠道回调接口。
// 控制台构造与真实渠道一致的带签名回调，调用与 POST /api/notify 相同的处理入口；
// 外部亦可通过 curl 直接调用 /api/notify 复现 成功/失败/重复/迟到 场景。

type mockPageData struct {
	Msg, Err string
	Payments []payment.MockPayment
	Refunds  []payment.MockRefund
}

func (s *Server) pageMock(w http.ResponseWriter, r *http.Request) {
	pays, err := s.Pay.ListMockPayments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	refunds, err := s.Pay.ListMockRefunds(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "mock.html", &mockPageData{
		Msg: r.FormValue("msg"), Err: r.FormValue("err"),
		Payments: pays, Refunds: refunds,
	})
}

// handleMockPay 模拟渠道受理支付并回调商城。
// action: success / fail / resend(重复通知，同交易号重发)
func (s *Server) handleMockPay(w http.ResponseWriter, r *http.Request) {
	payID := r.FormValue("paymentId")
	action := r.FormValue("action")
	txn := r.FormValue("txn")
	n, err := s.Pay.BuildNotify(r.Context(), payID, txn)
	if err != nil {
		redirectMsg(w, r, "/mock", "", errMessage(err))
		return
	}
	switch action {
	case "fail":
		n.Result = "fail"
	case "resend":
		// 保持 success + 同交易号 → 验证幂等
	}
	sig := s.Pay.Sign(*n)
	handled, err := s.Pay.HandleNotify(r.Context(), *n, sig)
	if err != nil {
		redirectMsg(w, r, "/mock", "", "回调结果 "+handled+": "+errMessage(err))
		return
	}
	redirectMsg(w, r, "/mock", "渠道回调完成，处理结果 "+handled, "")
}

// handleMockRefund 模拟渠道退款结果回调。action: success / fail
func (s *Server) handleMockRefund(w http.ResponseWriter, r *http.Request) {
	refundID := r.FormValue("refundId")
	success := r.FormValue("action") == "success"
	handled, err := s.Pay.HandleRefundResult(r.Context(), refundID, success, "渠道返回：模拟失败")
	if err != nil {
		redirectMsg(w, r, "/mock", "", errMessage(err))
		return
	}
	redirectMsg(w, r, "/mock", "退款回调完成，处理结果 "+handled, "")
}

// handleNotify 真实渠道回调接口：POST /api/notify
// 表单：txn, booking_id, amount, result, event_time(unix 秒), sig
func (s *Server) handleNotify(w http.ResponseWriter, r *http.Request) {
	eventUnix := parseInt64(r.FormValue("event_time"))
	if eventUnix == 0 {
		eventUnix = time.Now().Unix()
	}
	n := payment.Notify{
		ChannelTxn: r.FormValue("txn"),
		BookingID:  r.FormValue("booking_id"),
		Amount:     parseInt64(r.FormValue("amount")),
		Result:     r.FormValue("result"),
		EventTime:  time.Unix(eventUnix, 0).UTC(),
	}
	handled, err := s.Pay.HandleNotify(r.Context(), n, r.FormValue("sig"))
	if err != nil {
		http.Error(w, "handled="+handled+" err="+errMessage(err), http.StatusBadRequest)
		return
	}
	_, _ = w.Write([]byte("ok handled=" + handled))
}

// handleRefundNotify 渠道退款结果回调：POST /api/refund-notify
// 表单：refund_id, action(success/fail), sig（HMAC，与支付回调同密钥不同串）
func (s *Server) handleRefundNotify(w http.ResponseWriter, r *http.Request) {
	refundID := r.FormValue("refund_id")
	success := r.FormValue("action") == "success"
	if subtle.ConstantTimeCompare([]byte(r.FormValue("sig")), []byte(s.Pay.SignRefund(refundID, success))) != 1 {
		http.Error(w, "handled=sig_invalid err=退款回调验签失败", http.StatusBadRequest)
		return
	}
	handled, err := s.Pay.HandleRefundResult(r.Context(), refundID, success, "渠道返回：退款失败")
	if err != nil {
		http.Error(w, "handled="+handled+" err="+errMessage(err), http.StatusBadRequest)
		return
	}
	_, _ = w.Write([]byte("ok handled=" + handled))
}
