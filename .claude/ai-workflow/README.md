# ai-workflow 工具单元

可安装进**目标仓**的 AI 工作流工具，配合控制面（Go 后端 + Dashboard，见仓根 [`README.md`](../../README.md)）使用。

## 内容

| 件 | 作用 |
|----|------|
| `../skills/jira-to-issue/` | **B**：票 → 调查分析 → GitHub Issue「待审核」 |
| `../commands/issue-to-pr.md` | **C**：已审核 Issue → 蓝图 → 实装 → 测试 → Draft PR |
| `jira_api.py` | JIRA 客户端（读票 / ADF 解析 / 状态转换）；控制面与 skill 共用 |
| `emit-event.sh` | 阶段事件上报：skill 在阶段边界调用，把进度 POST 回控制面（点亮流水线/SSE/Slack） |
| `notify.sh` | Slack 通知小工具（可选；控制面已有 Slack sink，通常无需直接调用） |
| `install-into.sh` | 把上述单元装进一个新目标仓 |

## 运行模型

- **触发与编排**：由控制面 Go 后端负责（Dashboard 发起任务 → `git worktree` + `claude -p`）。
- **事件回传**：runner 向 worktree 注入 `WF_TASK_ID` / `WF_EVENT_URL` / `WF_INTERNAL_TOKEN`，`emit-event.sh` 据此回传。
- **凭据**：JIRA 等凭据写在控制面仓的 `.env`，runner 注入到 worktree；目标仓不存密钥。

> 旧的 webhook 单例接收器（`server.py` / `setup.sh` / `ensure-receiver.sh`）已退役，被控制面取代。

## 给新目标仓接入

```bash
.claude/ai-workflow/install-into.sh /path/to/目标仓
cd /path/to/目标仓 && git add .claude && git commit -m "chore: 接入 AI 工作流工具集"
# 再到控制面 config.json 的 repos 里登记本仓
```
