package model

import (
	"strings"
	"testing"
)

func TestNewID(t *testing.T) {
	a := NewID("B")
	b := NewID("B")
	if !strings.HasPrefix(a, "B") {
		t.Fatalf("缺少前缀: %s", a)
	}
	if len(a) <= len("B")+14 {
		t.Fatalf("单号长度不足（时间戳+随机后缀）: %s", a)
	}
	if a == b {
		t.Fatalf("随机后缀重复: %s", a)
	}
}

func TestActiveStatusesContainPaidStates(t *testing.T) {
	want := map[string]bool{
		BookingPendingPayment: false, BookingConfirmed: false,
		BookingCancelReview: false, BookingCheckedIn: false,
		BookingInService: false, BookingCompleted: false,
	}
	for _, st := range ActiveStatuses {
		if _, ok := want[st]; ok {
			want[st] = true
		}
	}
	for st, ok := range want {
		if !ok {
			t.Fatalf("有效占用状态缺少 %s", st)
		}
	}
	if len(ActiveStatuses) != len(want) {
		t.Fatalf("有效占用状态不应包含终态，got %v", ActiveStatuses)
	}
	// 终态不得占用名额
	for _, st := range []string{BookingCancelled, BookingNoShow, BookingClosed, BookingPaymentAnomaly} {
		for _, a := range ActiveStatuses {
			if a == st {
				t.Fatalf("终态 %s 不应在 ActiveStatuses 中", st)
			}
		}
	}
}
