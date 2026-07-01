// Package events 是进程内事件总线：事件先落库（store 为唯一真相），
// 再通知订阅者（SSE）与各 sink（Slack / HTTP 回调）。
// 订阅者收到的只是「有新事件」的 nudge，再回 store 拉，保证不丢事件。
package events

import (
	"sync"
	"time"

	"github.com/ryuclub/ai-workflow/internal/core/store"
)

// Sink 是事件的旁路消费者（Slack、HTTP 回调等）。Deliver 不应阻塞太久。
type Sink interface {
	Deliver(ev store.Event)
}

// Bus 串联 store、SSE 订阅者与 sink。
type Bus struct {
	st    store.Store
	mu    sync.Mutex
	subs  map[string]map[int]chan struct{} // taskID -> subID -> nudge
	next  int
	sinks []Sink
}

// NewBus 构造事件总线。
func NewBus(st store.Store, sinks ...Sink) *Bus {
	return &Bus{st: st, subs: map[string]map[int]chan struct{}{}, sinks: sinks}
}

// Publish 落库并广播一条事件。ev.TS 为空时填当前时间。
func (b *Bus) Publish(ev *store.Event) error {
	if ev.TS.IsZero() {
		ev.TS = time.Now()
	}
	if ev.Level == "" {
		ev.Level = "info"
	}
	if err := b.st.AppendEvent(ev); err != nil {
		return err
	}
	// 通知该任务的所有 SSE 订阅者（非阻塞）
	b.mu.Lock()
	for _, ch := range b.subs[ev.TaskID] {
		select {
		case ch <- struct{}{}:
		default: // 已有未消费 nudge，跳过——订阅者会一次性拉齐
		}
	}
	sinks := b.sinks
	b.mu.Unlock()
	for _, s := range sinks {
		s.Deliver(*ev)
	}
	return nil
}

// Subscribe 注册一个任务的 nudge 通道，返回订阅 ID 与通道。
func (b *Bus) Subscribe(taskID string) (int, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	id := b.next
	if b.subs[taskID] == nil {
		b.subs[taskID] = map[int]chan struct{}{}
	}
	ch := make(chan struct{}, 1)
	b.subs[taskID][id] = ch
	return id, ch
}

// Unsubscribe 注销订阅。
func (b *Bus) Unsubscribe(taskID string, id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if m := b.subs[taskID]; m != nil {
		delete(m, id)
		if len(m) == 0 {
			delete(b.subs, taskID)
		}
	}
}
