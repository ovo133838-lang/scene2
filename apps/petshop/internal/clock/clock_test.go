package clock

import (
	"testing"
	"time"
)

func TestFakeAdvance(t *testing.T) {
	c := NewFake(time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC))
	if got := c.Now(); !got.Equal(time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("初始时间不符: %s", got)
	}
	c.Advance(90 * time.Minute)
	if got := c.Now(); !got.Equal(time.Date(2026, 11, 14, 10, 30, 0, 0, time.UTC)) {
		t.Fatalf("推进后时间不符: %s", got)
	}
	c.Advance(-30 * time.Minute)
	if got := c.Now(); !got.Equal(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("回退后时间不符: %s", got)
	}
}

func TestRealNowUTC(t *testing.T) {
	before := time.Now().UTC().Add(-time.Minute)
	got := Real{}.Now()
	after := time.Now().UTC().Add(time.Minute)
	if got.Before(before) || got.After(after) {
		t.Fatalf("Real.Now 非当前时间: %s", got)
	}
	if got.Location() != time.UTC {
		t.Fatalf("Real.Now 应为 UTC，got %s", got.Location())
	}
}
