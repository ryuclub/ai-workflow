---
name: pr-reviewer
description: "审查 GitHub PR：检查现有 review comment 的处置情况，审查代码变更中的严重问题，resolve 已妥善处理的 thread，对新发现的问题添加评论，最后给出结论并 approve 或 request changes。触发时机: 审查PR / review PR / 看下这个PR / 帮我review"
user-invokable: true
---

# PR 审查工作流

## 概述

对指定的 GitHub PR 执行完整审查流程：检查现有 Copilot/reviewer 评论的处置情况，审查代码变更是否存在严重问题，给出结论并完成审批操作。

## 输入

- GitHub PR URL 或 PR 编号（必须）

## 工作流程

### Step 1: 收集 PR 信息

并行获取以下信息：

1. **PR 基本信息**：`gh pr view <number> --json title,body,headRefName,baseRefName,additions,deletions,changedFiles`
2. **现有 review comments**：`gh api repos/{owner}/{repo}/pulls/{number}/comments --paginate`，解析每条 comment 的作者、内容、回复关系
3. **PR diff**：`gh pr diff <number>`
4. **JIRA 工单信息**（如 PR 关联了 JIRA 工单）：从 PR title/body/分支名中提取工单号（如 PROJ-xxxx），通过 JIRA API 获取工单详情：
   ```bash
   curl --netrc https://your-domain.atlassian.net/rest/api/2/issue/PROJ-xxxx
   ```
   重点关注：工单描述、验收条件、子任务列表。用于后续审查时判断代码变更是否完整覆盖了工单需求、是否存在遗漏或超出范围的修改。

### Step 2: 评估现有 comment 处置情况

对每条 reviewer（Copilot 或其他 bot/人工）提出的 comment：

1. **查看作者回复**：通过 `in_reply_to_id` 关联回复
2. **判断处置是否妥当**：
   - **已修正**：作者回复称已修正，且 diff 中确认改动已落实 → 标记为可 resolve
   - **合理拒绝**：作者给出充分理由拒绝修改（如设计决策、场景限制） → 标记为可 resolve
   - **未妥善处理**：未回复、回复不充分、或声称已修但代码未改 → 需要跟进

### Step 3: 审查代码变更

基于 diff 内容，按以下优先级审查：

1. **严重问题**（阻塞合并）：
   - 并发安全：竞态条件、数据竞争、无条件覆盖状态
   - 数据一致性：事务范围不当、状态更新遗漏
   - 安全漏洞：注入、未授权访问、敏感信息泄露
   - 逻辑错误：条件判断错误、边界处理缺失
   - 测试用例缺口：核心逻辑变更但缺乏对应测试覆盖

2. **中等问题**（建议修改但不阻塞）：
   - 错误处理不完整
   - 日志规范不符合项目标准（参照 coding.md）
   - 变更超出工单范围

3. **轻微问题**（仅做记录）：
   - 代码风格
   - 可选优化

如需验证修复是否落实，fetch PR 分支后 `git show origin/<branch>:<file>` 查看实际代码。

### Step 4: 执行操作

#### 4.1 Resolve 已妥善处理的 thread

```bash
# 获取 thread ID
gh api graphql -f query='{ repository(owner:"...", name:"...") { pullRequest(number:N) { reviewThreads(first:20) { nodes { id isResolved comments(first:1) { nodes { databaseId author { login } } } } } } } }'

# Resolve thread
gh api graphql -f query='mutation { resolveReviewThread(input:{threadId:"..."}) { thread { isResolved } } }'
```

#### 4.2 对新发现的问题添加评论

- 使用 `gh api repos/{owner}/{repo}/pulls/{number}/reviews -X POST --input -` 提交 review
- 评论内容使用中文
- 说明问题的严重程度和建议的修复方向

#### 4.3 提交结论

根据审查结果：

- **无严重问题**：APPROVE，body 中简述审查结论（已确认的修复点、设计决策的合理性）
- **有严重问题**：REQUEST_CHANGES，body 中列出需要修复的问题
- **仅有非严重建议**：APPROVE，对建议项添加 comment 并注明"非严重问题，不阻塞合并"，然后 resolve

### Step 5: 对自己提出的非严重评论的处理

如果自己添加的评论属于非严重建议（不阻塞合并），应：

1. 回复自己的评论说明"非严重问题"
2. 直接 resolve 该 thread

避免留下未 resolve 的 thread 造成干扰。

## 输出格式

向用户汇报审查结果：

```
## Copilot/现有评论处置

| # | 文件 | 问题 | 处置 | 判断 |
|---|------|------|------|------|
| 1 | file.go | 问题摘要 | 已修正/合理拒绝/未处理 | 妥当/需跟进 |

## 新发现问题

（如有）列出新发现的严重/中等问题

## 结论

APPROVE / REQUEST_CHANGES + 理由
```

## 注意事项

- 评论语言：中文（遵循项目 CLAUDE.md 规则）
- 不添加 AI 工具相关署名或链接
- 审查重点放在逻辑正确性和数据一致性，而非代码风格
- 对于纯文档变更的 PR，重点审查文档描述与实际代码的一致性
