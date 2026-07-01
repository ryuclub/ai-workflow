// Command server 是 AI 工作流流水线的控制面：Gin HTTP 服务 + 任务编排。
//
// 需跑在能访问 claude 登录态的图形登录会话内（headless claude -p 的 OAuth 在 keychain）。
package main

import (
	"context"
	"log"
	"net"
	"os"
	"strconv"

	"github.com/ryuclub/ai-workflow/internal/api"
	"github.com/ryuclub/ai-workflow/internal/app"
	"github.com/ryuclub/ai-workflow/internal/core/events"
	"github.com/ryuclub/ai-workflow/internal/core/orchestrator"
	"github.com/ryuclub/ai-workflow/internal/core/runner"
	"github.com/ryuclub/ai-workflow/internal/core/secret"
	"github.com/ryuclub/ai-workflow/internal/core/store"
)

func main() {
	root := os.Getenv("WF_ROOT")
	if root == "" {
		root, _ = os.Getwd()
	}

	rt, err := app.NewRuntime(root)
	if err != nil {
		log.Fatalf("装配运行时失败: %v", err)
	}
	cfg := rt.Config("") // 全局模板：运行期操作键（DB 路径 / Slack / 端口等）

	st, err := store.OpenSQLite(cfg.DBPath())
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()
	if n, err := st.ReconcileInterrupted(); err == nil && n > 0 {
		log.Printf("启动对账：%d 个上次残留的运行中任务已标为待裁决", n)
	}

	// 凭据保险箱：MASTER_KEY 未配则为 nil（禁用凭据存储，令牌回落宿主登录态）。
	vault, err := secret.New(os.Getenv("MASTER_KEY"), st)
	if err != nil {
		log.Fatalf("初始化凭据保险箱失败: %v", err)
	}
	rt.SetVault(vault)
	rt.SetConfigStore(st)
	if vault == nil {
		log.Printf("⚠ 未设 MASTER_KEY：凭据存储禁用，Claude 令牌将回落宿主登录态（仅适合单机开发）")
	}

	// 无用户时 seed 首个租户+管理员，并迁移现有全局配置进该租户（须在 vault 就绪后）。
	if err := api.SeedBootstrap(st, cfg, vault, os.Getenv("WF_BOOTSTRAP_EMAIL"), os.Getenv("WF_BOOTSTRAP_PASSWORD"), os.Getenv("WF_BOOTSTRAP_TENANT")); err != nil {
		log.Fatalf("初始化管理员失败: %v", err)
	}

	bus := events.NewBus(st, events.NewSlackSink(cfg.SlackWebhook()))
	orch := orchestrator.New(rt, st, bus, runner.NewClaude(rt))
	orch.StartPoller(context.Background()) // 后台轮询 PR 审查决议，驱动修订循环

	srv := api.NewServer(rt, st, st, vault, bus, orch)
	addr := cfg.Host() + ":" + strconv.Itoa(cfg.Port())
	log.Printf("AI 工作流控制面启动 → http://%s  默认源=%s  多租户=on  模板登记仓=%v",
		addr, cfg.ActiveSource(), keys(cfg.Repos))
	if cfg.Host() == "0.0.0.0" {
		if vault == nil {
			log.Printf("⚠ 已对局域网开放（HOST=0.0.0.0）但未设 MASTER_KEY，凭据未加密存储，建议配置")
		}
		if ips := lanIPs(); len(ips) > 0 {
			log.Printf("局域网访问地址：http://%s:%d", ips[0], cfg.Port())
		}
	}
	if err := srv.Router().Run(addr); err != nil {
		log.Fatalf("服务退出: %v", err)
	}
}

// lanIPs 返回本机非回环的 IPv4 地址，用于打印局域网访问地址。
func lanIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() {
			if ip4 := ipn.IP.To4(); ip4 != nil {
				out = append(out, ip4.String())
			}
		}
	}
	return out
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
