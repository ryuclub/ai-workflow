# PR 工厂（控制面 + Dashboard）

把一张工单（JIRA / Linear）**自动**跑成 GitHub Issue → 人审 → 实装 → PR，并在 Dashboard 上**实时可视化**每个任务的流水线进度。多仓支持；人只在两端把关（审需求、合 PR）。

```
Dashboard 选票(+选仓) → 开始任务
  └─ B: 调查 → 建 GitHub Issue「待审核」
       └─ ⏸ 人审闸口（Dashboard 就地编辑 Issue → 通过/打回）
            └─ C: 建分支+蓝图 → 实装+自查 → 测试 → Draft PR + Issue「已实装」
人 review PR → 合并
```

- **控制面 = 本仓**：Go(Gin) 后端 + React 前端 + 任务编排 runner。
- **执行面 = 各目标仓**：被改代码的业务仓，装有 B/C 两个 skill；后端用 `git worktree` 在其中跑 `claude -p`。
- **票源**：JIRA 与 Linear **二选一**（配置选活跃源）。
- 完整设计见 [`docs/ai-workflow-rebuild-plan.md`](./docs/ai-workflow-rebuild-plan.md)。

## 架构

```
cmd/server/main.go        启动 Gin，组装依赖
internal/api/             薄 HTTP 适配层：/api/v1（公共契约）+ /internal（skill 事件回传）
internal/core/            纯领域层（禁依赖 gin）
  ├─ config/              config.json + .env（进程env > .env > 默认）
  ├─ source/              Provider 抽象 + jira / linear 实现（归一化 Ticket）
  ├─ github/              gh 封装（取/改 Issue、切 label）—— 人审就地编辑用
  ├─ pipeline/            DAG 数据模型（当前固定 8 节点流水线）
  ├─ orchestrator/        状态机：B → 等人审 → C；失败 → 待裁决
  ├─ runner/              worktree + claude -p（注入 WF_TASK_ID/事件URL/token）
  ├─ events/             事件总线 + 可 fan-out sink（SSE / Slack）
  └─ store/               接口 + SQLite 实现
frontend/                 React + Vite + React Flow Dashboard
.claude/                  可安装到目标仓的工具单元（jira_api.py / emit-event.sh / B、C skill）
```

## 运行

### 依赖
- Go 1.26+、Node 20+（仅构建前端）
- `claude` CLI（**须在能访问 keychain 的图形登录会话内**，否则 headless `claude -p` 会「未登录假成功」）
- `gh` CLI（已登录，需 repo 权限）
- Python 3（`jira_api.py` 运行时）

### 配置
```bash
cp config.example.json config.json          # 登记目标仓 + 选 source（机器相关，已 gitignore）
cp .claude/ai-workflow/.env.example .claude/ai-workflow/.env
# 填 .env：票源凭据（JIRA 或 Linear）、INTERNAL_TOKEN（openssl rand -hex 32）、PORT 等
```

### 构建前端 + 启动后端（生产式：后端托管 SPA）
```bash
cd frontend && npm install && npm run build && cd ..
go run ./cmd/server           # 默认 127.0.0.1:8788，直接打开该地址即是 Dashboard
```

### 前端热更新开发（可选）
```bash
go run ./cmd/server                          # 后端
cd frontend && npm run dev                   # 前端 :5173，/api 代理到后端
# 后端端口非 8788 时：VITE_BACKEND=http://127.0.0.1:<PORT> npm run dev
```

### 常用命令
```bash
go build ./... && go vet ./... && gofmt -l .   # 后端
cd frontend && npm run build                   # 前端类型检查 + 构建
```

## 使用说明（Dashboard）

打开 http://127.0.0.1:8788（或你的 `PORT`）：

1. **候选票（入口）**：左栏列出活跃票源（JIRA/Linear）的工单。
   - **搜索**：输入票号（`PROJ-3250` / `3250`）或标题关键词；空着点「刷新」回到默认最近列表。候选票与上次搜索**本地持久化**，切 tab / 刷新页面不丢。
   - **票号**可点，在新标签打开原始 JIRA/Linear 链接。
   - 每条票自动**预填建议仓**（按标题 `[repo]` 标记或关键词路由），按需改选后点「**开始任务**」。
   - 已起过的票：进行中显示「进行中 →」（挡住重复起）；已结束显示「重新开始」+「上次↗」。
2. **任务**：左栏「任务」tab，每条显示 `#编号`（口头引用用）/ 票号 / 标题 / 状态。点开进详情。
3. **流水线视图**（右侧，可拖拽缩放）：
   - 步骤节点按状态着色：待跑灰 / 运行中橙(发光) / 完成绿 / 失败红 / 等人审蓝；
   - **终点节点**（实心渐变，三色）：**完成=绿** / **无需处理=蓝** / **待裁决·异常=红**，命中即点亮；
   - **票详情**面板（表头即票标题）：展开看源工单状态/正文。
4. **人审闸口**：任务停在「等人审」时，下方出现 Issue 编辑器——就地改标题/正文（保存即写回 GitHub），点「**通过**」启动实装（C），或「**打回**」转待裁决。
5. **取消**：运行中任务详情右上角「取消任务」可随时终止（杀掉 claude 子进程）。
6. **终态含义**：`完成`(出 PR) / `已跳过`(正常·无需处理) / `待裁决`(异常·需人工) / `已取消` / `已打回`。
7. 侧栏可**收起**（顶栏 `‹`）或**拖拽调宽**，状态本地保存。

## 给目标仓接入 AI 能力 / 工作隔离模型

**无需手动克隆，也不碰你的开发目录**：

- 控制面为每个登记仓**自管一份「主仓」克隆**（在 `ReposDir` 下，按需 `gh repo clone` / token 克隆，用前 `fetch`）。
- **每个任务在主仓之外的独立 `git worktree` 里干活**（`WorktreeBase` 下），跑完即清理——主仓工作树从不被任务改动。
- runner 在该 worktree 里**临时注入** B/C skill 包 + 凭据，跑完随 worktree 清理。

接入一个仓只需在设置页（或 `config.json`）按 **GitHub 地址** 登记（`github` / 标题关键词 `match` / 可选 `base`）。私有仓克隆走 `gh` 登录态或 `GITHUB_TOKEN`。

> 如希望 skill **常驻**到目标仓（便于人工直接调用），可选 `.claude/ai-workflow/install-into.sh /path/to/目标仓`，但自动化流程不依赖它。

## JIRA/Linear 状态联动（可选）

在 `config.json` 加 `status_map`（任务态 → 票源流转名）即可在每次状态流转时回写工单状态；**默认空=不联动**（流转名是项目特定的，写错会污染真实票）：

```json
"status_map": { "running_b": "开发中", "awaiting_review": "待确认", "running_c": "实装中", "done": "完成" }
```

## 真相源优先级

见 [`CLAUDE.md`](./CLAUDE.md)。
