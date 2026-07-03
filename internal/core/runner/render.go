package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// renderWriter 把 claude 的 stream-json 输出逐行翻译成人类可读文本写入下游。
// 背景：`claude -p` 纯文本模式只在结束时打印最终结果，运行期毫无输出——直播/日志
// 都看不到进度；改用 stream-json 后由本翻译器实时渲染每个事件（工具调用/文本/结束）。
// 非 JSON 行（stderr 告警等）原样透传。
type renderWriter struct {
	dst   io.Writer
	carry []byte // 半行缓冲（Write 可能切在行中间）
}

func newRenderWriter(dst io.Writer) *renderWriter { return &renderWriter{dst: dst} }

func (w *renderWriter) Write(p []byte) (int, error) {
	w.carry = append(w.carry, p...)
	for {
		idx := bytes.IndexByte(w.carry, '\n')
		if idx < 0 {
			break
		}
		line := w.carry[:idx]
		w.carry = w.carry[idx+1:]
		w.render(line)
	}
	return len(p), nil
}

// Flush 渲染残留的最后半行（进程结束后调用）。
func (w *renderWriter) Flush() {
	if len(w.carry) > 0 {
		w.render(w.carry)
		w.carry = nil
	}
}

func (w *renderWriter) out(s string) { _, _ = io.WriteString(w.dst, s) }

func (w *renderWriter) render(line []byte) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return
	}
	if trimmed[0] != '{' { // 非 JSON（stderr 告警等）原样透传
		w.out(string(trimmed) + "\n")
		return
	}
	var msg struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message struct {
			Content []struct {
				Type    string          `json:"type"`
				Text    string          `json:"text"`
				Name    string          `json:"name"`
				Input   json.RawMessage `json:"input"`
				Content json.RawMessage `json:"content"` // tool_result
			} `json:"content"`
		} `json:"message"`
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}
	if json.Unmarshal(trimmed, &msg) != nil {
		w.out(string(trimmed) + "\n") // 解析不了就透传，宁可噪音不可丢信息
		return
	}
	switch msg.Type {
	case "system":
		if msg.Subtype == "init" {
			w.out("── claude 会话启动 ──\n")
		}
	case "assistant":
		for _, c := range msg.Message.Content {
			switch c.Type {
			case "text":
				if t := strings.TrimSpace(c.Text); t != "" {
					w.out(t + "\n")
				}
			case "tool_use":
				w.out(fmt.Sprintf("⚒ %s %s\n", c.Name, compact(c.Input, 200)))
			}
		}
	case "user": // 工具结果：只给一行摘要，避免整页原文刷屏
		for _, c := range msg.Message.Content {
			if c.Type == "tool_result" {
				w.out("↳ " + compact(c.Content, 160) + "\n")
			}
		}
	case "result":
		status := "完成"
		if msg.IsError {
			status = "出错"
		}
		w.out("── 运行" + status + " ──\n")
		if t := strings.TrimSpace(msg.Result); t != "" {
			w.out(t + "\n")
		}
	default:
		// 其它事件（stream_event 等）忽略：行级 assistant 消息已够直播粒度
	}
}

// compact 把 JSON 片段压成单行摘要（截断到 n 字节，多字节安全）。
func compact(raw json.RawMessage, n int) string {
	s := strings.Join(strings.Fields(string(raw)), " ")
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !isRuneStart(cut[len(cut)-1]) {
		cut = cut[:len(cut)-1] // 别切在多字节字符中间
	}
	if len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
