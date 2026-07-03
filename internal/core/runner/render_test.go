package runner

import (
	"strings"
	"testing"
)

// stream-json → 可读行：文本/工具调用/结束标记渲染，非 JSON 透传，跨 Write 半行拼接。
func TestRenderWriter(t *testing.T) {
	var sb strings.Builder
	w := newRenderWriter(&sb)

	lines := []string{
		`{"type":"system","subtype":"init","session_id":"x"}`,
		`Ignoring 2 permissions.allow entries`, // 非 JSON stderr 告警：透传
		`{"type":"assistant","message":{"content":[{"type":"text","text":"开始调查"},{"type":"tool_use","name":"Bash","input":{"command":"git log"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"commit abc123"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"完成建 Issue"}`,
	}
	payload := strings.Join(lines, "\n") + "\n"
	// 模拟跨 Write 边界的半行切割
	half := len(payload) / 2
	_, _ = w.Write([]byte(payload[:half]))
	_, _ = w.Write([]byte(payload[half:]))
	w.Flush()

	out := sb.String()
	for _, want := range []string{
		"── claude 会话启动 ──",
		"Ignoring 2 permissions.allow entries",
		"开始调查",
		"⚒ Bash",
		"git log",
		"↳",
		"── 运行完成 ──",
		"完成建 Issue",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q：\n%s", want, out)
		}
	}
}
