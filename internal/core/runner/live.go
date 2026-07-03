package runner

import (
	"io"
	"sync"
	"time"
)

// liveBufMax 是单任务直播缓冲上限：超过即停止追加（防失控任务撑爆内存），
// 订阅者偏移量保持稳定；全量输出仍完整落盘（writeLog）。
const liveBufMax = 1 << 20 // 1MB

// Live 是任务 claude 输出的内存直播器：runner 边跑边写，SSE 订阅者按偏移增量读。
// 只服务「运行中」的任务——跑完即弃，历史输出走落盘日志（问塔台 read_task_log）。
// 支持两种订阅：按任务（旧端点）与按租户（合流端点：一条连接看本租户全部直播）。
type Live struct {
	mu         sync.Mutex
	streams    map[string]*liveStream           // taskID -> stream
	tenantSubs map[string]map[int]chan struct{} // tenantID -> subID -> nudge
	next       int
}

type liveStream struct {
	tenantID  string
	label     string // B / C / D
	buf       []byte
	truncated bool
	done      bool
	subs      map[int]chan struct{} // nudge：有新数据/已结束
}

// NewLive 构造直播器。
func NewLive() *Live {
	return &Live{streams: map[string]*liveStream{}, tenantSubs: map[string]map[int]chan struct{}{}}
}

// liveWriter 是绑定某任务的 io.Writer（cmd.Stdout/Stderr 用，跨 goroutine 安全）。
type liveWriter struct {
	l      *Live
	taskID string
}

func (w liveWriter) Write(p []byte) (int, error) {
	w.l.mu.Lock()
	s := w.l.streams[w.taskID]
	if s != nil && !s.truncated {
		if len(s.buf)+len(p) > liveBufMax {
			s.buf = append(s.buf, []byte("\n…[输出过长，直播截断；完整日志见落盘文件]\n")...)
			s.truncated = true
		} else {
			s.buf = append(s.buf, p...)
		}
		for _, ch := range s.subs {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
		w.l.nudgeTenantLocked(s.tenantID)
	}
	w.l.mu.Unlock()
	return len(p), nil
}

// nudgeTenantLocked 通知租户级订阅者（调用方须持有 l.mu）。
func (l *Live) nudgeTenantLocked(tenantID string) {
	for _, ch := range l.tenantSubs[tenantID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Start 开启某任务的直播流，返回可并发写入的 Writer。
func (l *Live) Start(taskID, tenantID, label string) io.Writer {
	l.mu.Lock()
	l.streams[taskID] = &liveStream{tenantID: tenantID, label: label, subs: map[int]chan struct{}{}}
	l.mu.Unlock()
	return liveWriter{l: l, taskID: taskID}
}

// End 结束直播：通知订阅者收尾。流保留 1 分钟宽限期后回收——
// 租户级合流订阅者不逐流注册，需要宽限期读走尾部数据并发收尾事件。
func (l *Live) End(taskID string) {
	l.mu.Lock()
	s := l.streams[taskID]
	if s == nil {
		l.mu.Unlock()
		return
	}
	s.done = true
	for _, ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	l.nudgeTenantLocked(s.tenantID)
	l.mu.Unlock()
	time.AfterFunc(time.Minute, func() {
		l.mu.Lock()
		if cur := l.streams[taskID]; cur == s {
			delete(l.streams, taskID)
		}
		l.mu.Unlock()
	})
}

// Subscribe 订阅某任务的直播（带租户校验）。ok=false 表示该任务当前无直播（未在跑）。
func (l *Live) Subscribe(taskID, tenantID string) (id int, nudge <-chan struct{}, label string, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.streams[taskID]
	if s == nil || s.tenantID != tenantID {
		return 0, nil, "", false
	}
	l.next++
	ch := make(chan struct{}, 1)
	s.subs[l.next] = ch
	return l.next, ch, s.label, true
}

// Unsubscribe 注销订阅（流本体由 End 的宽限期定时回收）。
func (l *Live) Unsubscribe(taskID string, id int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.streams[taskID]; s != nil {
		delete(s.subs, id)
	}
}

// OutputRef 标识一条直播流。
type OutputRef struct {
	TaskID string
	Label  string
	Done   bool
}

// ActiveOutputs 返回该租户当前存在的直播流（含刚结束、处于宽限期待收尾的）。
func (l *Live) ActiveOutputs(tenantID string) []OutputRef {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []OutputRef
	for id, s := range l.streams {
		if s.tenantID == tenantID {
			out = append(out, OutputRef{TaskID: id, Label: s.label, Done: s.done})
		}
	}
	return out
}

// SubscribeTenantOutput 订阅该租户全部任务直播的 nudge（合流端点用）。
func (l *Live) SubscribeTenantOutput(tenantID string) (int, <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	ch := make(chan struct{}, 1)
	if l.tenantSubs[tenantID] == nil {
		l.tenantSubs[tenantID] = map[int]chan struct{}{}
	}
	l.tenantSubs[tenantID][l.next] = ch
	return l.next, ch
}

// UnsubscribeTenantOutput 注销租户级直播订阅。
func (l *Live) UnsubscribeTenantOutput(tenantID string, id int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if m := l.tenantSubs[tenantID]; m != nil {
		delete(m, id)
		if len(m) == 0 {
			delete(l.tenantSubs, tenantID)
		}
	}
}

// Read 从偏移量读增量。done=true 表示任务已结束（读完本次 chunk 即可收尾）。
func (l *Live) Read(taskID string, from int) (chunk []byte, next int, done, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.streams[taskID]
	if s == nil {
		return nil, from, true, false
	}
	if from < len(s.buf) {
		chunk = append([]byte(nil), s.buf[from:]...)
	}
	return chunk, len(s.buf), s.done, true
}
