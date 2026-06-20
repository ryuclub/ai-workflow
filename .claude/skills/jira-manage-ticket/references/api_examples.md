# JIRA REST API CRUD 示例

## 认证

所有请求使用 Basic 认证:

```bash
# 从 .env 读取
# ATLASSIAN_USERNAME / ATLASSIAN_API_KEY / ATLASSIAN_DOMAIN
USER=$ATLASSIAN_USERNAME
TOKEN=$ATLASSIAN_API_KEY
DOMAIN=$ATLASSIAN_DOMAIN

Authorization: Basic base64($USER:$TOKEN)
```

## Read (GET)

### 获取工单

```bash
curl -s -u "$USER:$TOKEN" \
  "https://$DOMAIN/rest/api/3/issue/MOS-1234" | jq
```

### 仅获取特定字段

```bash
curl -s -u "$USER:$TOKEN" \
  "https://$DOMAIN/rest/api/3/issue/MOS-1234?fields=summary,status,subtasks" | jq
```

## Create (POST)

### 创建子任务

```bash
curl -s -u "$USER:$TOKEN" \
  -X POST \
  -H "Content-Type: application/json" \
  "https://$DOMAIN/rest/api/3/issue" \
  -d '{
    "fields": {
      "project": {"key": "MOS"},
      "parent": {"key": "MOS-1234"},
      "summary": "子任务标题",
      "issuetype": {"name": "子任务"},
      "description": {
        "type": "doc",
        "version": 1,
        "content": [
          {"type": "paragraph", "content": [{"type": "text", "text": "说明内容"}]}
        ]
      },
      "timetracking": {
        "originalEstimate": "4h",
        "remainingEstimate": "4h"
      },
      "duedate": "2026-02-18",
      "labels": ["Team_Feature"]
    }
  }'
```

**字段说明:**

- `timetracking.originalEstimate`: 预估工时（例: "4h", "1d"）
- `timetracking.remainingEstimate`: 剩余工时（例: "4h", "1d"）
- `description`: Atlassian Document Format (ADF) 格式

## Update (PUT)

### 更新标题

```bash
curl -s -u "$USER:$TOKEN" \
  -X PUT \
  -H "Content-Type: application/json" \
  "https://$DOMAIN/rest/api/3/issue/MOS-1234" \
  -d '{"fields": {"summary": "新标题"}}'
```

### 更新描述

```bash
curl -s -u "$USER:$TOKEN" \
  -X PUT \
  -H "Content-Type: application/json" \
  "https://$DOMAIN/rest/api/3/issue/MOS-1234" \
  -d '{
    "fields": {
      "description": {
        "type": "doc",
        "version": 1,
        "content": [
          {"type": "paragraph", "content": [{"type": "text", "text": "新描述内容"}]}
        ]
      }
    }
  }'
```

### 设置负责人

```bash
# 获取账户 ID
curl -s -u "$USER:$TOKEN" \
  "https://$DOMAIN/rest/api/3/user/search?query=user@example.com" | jq '.[0].accountId'

# 设置负责人
curl -s -u "$USER:$TOKEN" \
  -X PUT \
  -H "Content-Type: application/json" \
  "https://$DOMAIN/rest/api/3/issue/MOS-1234" \
  -d '{"fields": {"assignee": {"accountId": "xxx"}}}'
```

## Delete (DELETE)

```bash
curl -s -u "$USER:$TOKEN" \
  -X DELETE \
  "https://$DOMAIN/rest/api/3/issue/MOS-1234"
```

**注意:** 如果存在子任务，需先删除子任务

## Transition (状态变更)

### 获取可用的状态转换

```bash
curl -s -u "$USER:$TOKEN" \
  "https://$DOMAIN/rest/api/3/issue/MOS-1234/transitions" | jq '.transitions[] | {id, name}'
```

### 执行状态变更

```bash
curl -s -u "$USER:$TOKEN" \
  -X POST \
  -H "Content-Type: application/json" \
  "https://$DOMAIN/rest/api/3/issue/MOS-1234/transitions" \
  -d '{"transition": {"id": "31"}}'
```
