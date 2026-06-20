---
name: pr-creator
description: "根据 Git diff 和 JIRA 工单信息自动生成 PR 描述文，并一键创建 PR（Draft）。按照项目 PR 模板的格式，整理变更内容、背景说明和测试结果。根据分支名自动判定并附加 PR 标签。触发时机: 创建PR / 写PR描述 / 生成Pull Request说明 / 给这个分支创建PR / 创建PROJ-xxx的PR"
user-invokable: true
---

# PR 创建器

## 概述

基于 Git 变更差异和 JIRA 工单信息，按照项目标准 PR 模板生成描述文，并一键完成 PR（Draft）创建。
根据分支名自动判定 PR 标签，并在创建时附加。

## 工作流程

### Step 1: 信息收集

1. **确认 JIRA 工单号**
   - 从用户获取工单号（例: PROJ-1234）
   - 如有多个工单则全部确认

2. **确认目标分支**
   - 向用户确认合并目标（基础分支）
   - 常用基础分支:
     - main: 通用修改、技术债务、hotfix
     - stage: 预发布分支
     - dev: 开发分支

3. **确认 PR 创建模式**
   - 向用户确认以下两点:
     - **PR 创建方式**: 一键创建 PR（A 模式）还是仅生成描述文（B 模式）
     - **PR 类型**: Draft PR 还是正式 PR（仅 A 模式需确认，默认为 Draft PR）

4. **掌握变更内容**
   ```bash
   # 将基础分支作为变量使用（例: main 或 stage）
   BASE_BRANCH=main

   # 变更文件列表
   git diff --name-only ${BASE_BRANCH}...HEAD

   # 变更内容详情
   git diff ${BASE_BRANCH}...HEAD

   # 提交历史
   git log ${BASE_BRANCH}..HEAD --oneline
   ```

### Step 2: 读取模板

读取 PR 模板文件。

**模板路径**: .github/PULL_REQUEST_TEMPLATE.md

**重要**: 必须从上述文件直接读取模板。不在 skill 内保存副本，始终引用最新模板。

**注意**: 如果模板文件不存在，则使用以下默认结构:

```markdown
## 关联工单
- [PROJ-XXXX](https://<org>.atlassian.net/browse/PROJ-XXXX)

## 变更概要
<!-- 简要说明本次变更的目的和范围 -->

## 变更内容
<!-- 列出主要的变更点 -->

## 影响范围
<!-- 说明本次变更影响的模块/功能 -->

## 测试
<!-- 测试方法和结果 -->

## 备注
<!-- 其他需要说明的事项 -->
```

### Step 3: 生成 PR 描述文和标签

1. 按照读取的模板结构填写各部分
2. 从 git diff 中提取主要变更，写入「变更内容」
3. 根据 JIRA 工单和会话内容描述「变更概要」
4. 如有测试执行结果则粘贴到「测试」部分
5. **根据分支名判定 PR 标签**（参见下方「标签判定规则」）

### Step 4: 输出

1. **提出 PR 标题方案**
   - 根据分支名前缀确定 PR 标题前缀（变更类型定义参照 [branch.md](../../guidelines/branch.md)）
   - 判断困难时向用户确认

2. **展示推荐标签**
   - 显示根据标签判定规则确定的标签

3. **以 Markdown 格式输出 PR 描述文**

4. 根据需要修改和调整

5. **创建 PR**（仅 A 模式）
   - 获得用户确认后，执行以下命令创建 PR:
   ```bash
   # 推送分支
   git push -u origin <current-branch>

   # 创建 PR（--draft 仅在指定 Draft PR 时附加）
   gh pr create \
     --base <base-branch> \
     --title "<PR标题>" \
     --body "<PR描述文>" \
     --label "<判定的标签>" \
     [--draft]
   ```
   - B 模式则输出 PR 标题、推荐标签和 PR 描述文后结束

## 标签判定规则

根据分支名前缀自动判定 PR 标签。
变更类型与分支前缀的对应关系参照 [branch.md](../../guidelines/branch.md)。

**标签映射**: 分支前缀 → PR 标签（feat→feature, fix→bugfix, hotfix→bugfix, perf→performance, 其余同名）

**不匹配时**: 如果不匹配任何已定义模式，根据变更内容推测最合适的标签并向用户确认。

## 编写指南

如果 PR 模板文件（.github/PULL_REQUEST_TEMPLATE.md）存在，其中 HTML 注释（<!-- -->）包含各部分的编写指南。读取模板时按照注释的指示填写各部分。

如果模板不存在，按照 Step 2 中的默认结构进行编写。
