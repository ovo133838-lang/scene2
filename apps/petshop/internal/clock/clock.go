// Package clock 提供可配置教学时钟：生产使用真实时间，测试可手动推进，
// 保证测试与演示使用同一业务规则（题面第一部分 §1.8）。
package clock

import (
	"sync"
	"time"
)

// Clock 是业务层获取当前时间的唯一入口，禁止在业务代码中直接调用 time.Now()。
type Clock interface {
	Now() time.Time
}

// Real 真实时钟（UTC）。
type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC() }

// Fake 可手动推进的时钟，用于单元/并发测试与缩短时长演示。
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

func NewFake(t time.Time) *Fake { return &Fake{t: t.UTC()} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Advance 将时钟向前推进 d（测试与演示用）。
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}
