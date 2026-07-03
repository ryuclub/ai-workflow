package runner

import (
	"strings"
	"testing"
)

// 直播器核心语义：写入→订阅→增量读→结束→回收。
func TestLiveStream(t *testing.T) {
	l := NewLive()
	w := l.Start("t1", "tenant-a", "C")

	if _, err := w.Write([]byte("hello ")); err != nil {
		t.Fatal(err)
	}

	// 租户不匹配不可订阅
	if _, _, _, ok := l.Subscribe("t1", "tenant-b"); ok {
		t.Fatal("跨租户订阅应被拒绝")
	}
	id, nudge, label, ok := l.Subscribe("t1", "tenant-a")
	if !ok || label != "C" {
		t.Fatalf("订阅失败: ok=%v label=%q", ok, label)
	}

	// 快照读：拿到已有输出
	chunk, off, done, ok := l.Read("t1", 0)
	if !ok || done || string(chunk) != "hello " {
		t.Fatalf("快照读异常: %q done=%v ok=%v", chunk, done, ok)
	}

	// 增量写 → nudge → 增量读
	if _, err := w.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	<-nudge
	chunk, off, done, _ = l.Read("t1", off)
	if string(chunk) != "world" || done {
		t.Fatalf("增量读异常: %q done=%v", chunk, done)
	}

	// 结束 → done=true；宽限期内仍可订阅读尾部（合流端点靠此收尾），流由定时器回收
	l.End("t1")
	if _, _, done, _ = l.Read("t1", off); !done {
		t.Fatal("End 后应 done")
	}
	l.Unsubscribe("t1", id)
	if _, _, _, ok := l.Subscribe("t1", "tenant-a"); !ok {
		t.Fatal("宽限期内应仍可订阅（读尾部数据）")
	}
	refs := l.ActiveOutputs("tenant-a")
	if len(refs) != 1 || !refs[0].Done {
		t.Fatalf("宽限期内应列出已结束流: %+v", refs)
	}
}

// 超上限截断：停止追加并留提示，偏移量保持稳定。
func TestLiveTruncate(t *testing.T) {
	l := NewLive()
	w := l.Start("t2", "tenant-a", "B")
	big := strings.Repeat("x", liveBufMax)
	_, _ = w.Write([]byte(big))
	_, _ = w.Write([]byte("overflow"))
	chunk, _, _, _ := l.Read("t2", 0)
	if !strings.Contains(string(chunk), "直播截断") {
		t.Fatal("超限应带截断提示")
	}
	if strings.Contains(string(chunk), "overflow") {
		t.Fatal("超限后不应继续追加")
	}
}
