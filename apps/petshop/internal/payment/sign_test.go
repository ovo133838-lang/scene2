package payment

import (
	"testing"
	"time"
)

func TestSignStableAndFieldSensitive(t *testing.T) {
	s := &Service{Secret: "k"}
	n := Notify{ChannelTxn: "T1", BookingID: "B1", Amount: 9900, Result: "success", EventTime: time.Unix(1700000000, 0).UTC()}
	sig := s.Sign(n)
	if sig == "" || len(sig) != 64 {
		t.Fatalf("签名异常: %q", sig)
	}
	if s.Sign(n) != sig {
		t.Fatalf("同内容签名应稳定")
	}
	for _, mut := range []Notify{
		{ChannelTxn: "T2", BookingID: "B1", Amount: 9900, Result: "success", EventTime: n.EventTime},
		{ChannelTxn: "T1", BookingID: "B2", Amount: 9900, Result: "success", EventTime: n.EventTime},
		{ChannelTxn: "T1", BookingID: "B1", Amount: 1, Result: "success", EventTime: n.EventTime},
		{ChannelTxn: "T1", BookingID: "B1", Amount: 9900, Result: "fail", EventTime: n.EventTime},
		{ChannelTxn: "T1", BookingID: "B1", Amount: 9900, Result: "success", EventTime: time.Unix(1700000001, 0).UTC()},
	} {
		if s.Sign(mut) == sig {
			t.Fatalf("字段变化必须改变签名: %+v", mut)
		}
	}
}

func TestSignRefund(t *testing.T) {
	s := &Service{Secret: "k"}
	ok := s.SignRefund("R1", true)
	if ok == "" || len(ok) != 64 {
		t.Fatalf("退款签名异常: %q", ok)
	}
	if s.SignRefund("R1", true) != ok {
		t.Fatalf("同内容签名应稳定")
	}
	if s.SignRefund("R1", false) == ok || s.SignRefund("R2", true) == ok {
		t.Fatalf("退款单号/结果变化必须改变签名")
	}
	if ok == s.Sign(Notify{ChannelTxn: "R1", BookingID: "", Amount: 0, Result: "true"}) {
		t.Fatalf("退款签名不应与支付签名同构")
	}
}
