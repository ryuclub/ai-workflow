# JIRA 工单规范

## 项目信息

- **项目 Key**: `MOS`
- **工单 URL**: `https://mosavi.atlassian.net/browse/MOS-XXXX`
- **看板**: https://mosavi.atlassian.net/jira/software/c/projects/MOS/boards/170

## 工单类型

| 类型 | 用途 |
| ---- | ---- |
| `长篇故事` | Epic，跨多个 Sprint 的大功能 |
| `故事` | 用户故事，一个迭代内可完成的功能 |
| `任务` | 技术任务、chore、docs 等 |
| `缺陷` | Bug 修复 |
| `子任务` | 归属于父工单的细分任务 |

## 创建工单时的必填字段

所有新建工单必须包含以下字段：

| 字段 | 值 |
| ---- | ---- |
| **系统**（customfield_10037） | `Server` |
| **修复版本**（fixVersions） | 最新未发布的 Server 版本（如 `Server-1.8.1`） |
| **担当**（assignee） | 创建人自己 |

## 使用脚本创建工单

工具路径：`.claude/skills/jira-manage-ticket/scripts/jira_api.py`

```bash
# 创建独立工单（故事/任务/缺陷）
python3 .claude/skills/jira-manage-ticket/scripts/jira_api.py \
  create-task "<标题>" "<描述>" [预估工时h] [工单类型]

# 示例：创建故事
python3 .claude/skills/jira-manage-ticket/scripts/jira_api.py \
  create-task "补全 Controller Swagger 注释" "为 health/discover 等接口添加 godoc 注释" 4 故事

# 创建子工单
python3 .claude/skills/jira-manage-ticket/scripts/jira_api.py \
  create MOS-1234 "<子任务标题>" "<描述>" [预估工时h]

# 获取工单信息
python3 .claude/skills/jira-manage-ticket/scripts/jira_api.py get MOS-1234

# 搜索工单（JQL）
python3 .claude/skills/jira-manage-ticket/scripts/jira_api.py \
  search "project = MOS AND assignee = currentUser() AND status != Done"

# 状态变更
python3 .claude/skills/jira-manage-ticket/scripts/jira_api.py \
  transition MOS-1234 "进行中"
```

> 凭据配置：`.claude/skills/jira-manage-ticket/.env`（参照 `.env.example`）

## JIRA 评论格式

JIRA 评论使用 **JIRA Wiki 标记**，禁止使用 Markdown。

| 要素 | 写法 |
| ---- | ---- |
| 二级标题 | `h2. 标题` |
| 三级标题 | `h3. 标题` |
| 有序列表 | `# 项目` |
| 无序列表 | `* 项目` |
| 行内代码 | `{{code}}` |
| 粗体 | `*text*` |
| 链接 | `[显示文本\|URL]` |

## 工单与分支的对应

一个工单对应一个分支，分支命名规则参照 [branch.md](branch.md)。

```
故事 MOS-1234
  └── feat/MOS-1234-add-user-auth
        └── PR → stage
```
