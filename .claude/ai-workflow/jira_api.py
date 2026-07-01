#!/usr/bin/env python3
"""
JIRA REST API CRUD 操作脚本

使用方法:
  python jira_api.py <command> [options]

命令:
  get <ticket_key>                              获取工单
  create-task <summary> [desc] [hours] [issue_type]            创建独立工单
  create <parent_key> <summary> [desc] [hours] [issue_type]    创建子工单
  bulk-create <parent_key> <json>               批量创建子工单
  update <ticket_key> <json_fields>             更新字段
  delete <ticket_key>                           删除工单
  transition <ticket_key> <transition_name>     状态变更
  search <jql>                                  JQL 搜索

环境变量（从 .env 文件读取）:
  ATLASSIAN_USERNAME  用户名（邮箱地址）
  ATLASSIAN_API_KEY   API Token
  ATLASSIAN_DOMAIN    域名
"""

import json
import os
import sys
from pathlib import Path
from typing import Optional, Union
import urllib.request
import urllib.error
import urllib.parse
import base64


# 默认引用 skill 目录下的 .env
# ai-workflow 工具集自包含：.env 与本脚本同目录
DEFAULT_ENV_PATH = Path(__file__).resolve().parent / ".env"


def load_env(env_path: str = None) -> dict:
    """从 .env 文件加载环境变量"""
    path = Path(env_path) if env_path else DEFAULT_ENV_PATH
    env = {}
    if path.exists():
        with open(path, encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if line and not line.startswith("#") and "=" in line:
                    key, value = line.split("=", 1)
                    value = value.strip()
                    # 去掉首尾成对引号（兼容 shell/dotenv 风格的带引号值）
                    if len(value) >= 2 and value[0] == value[-1] and value[0] in ("'", '"'):
                        value = value[1:-1]
                    env[key.strip()] = value
    return env


class JiraAPI:
    def __init__(self):
        env = load_env()
        self.username = env.get("ATLASSIAN_USERNAME") or os.environ.get("ATLASSIAN_USERNAME")
        self.api_key = env.get("ATLASSIAN_API_KEY") or os.environ.get("ATLASSIAN_API_KEY")
        self.domain = env.get("ATLASSIAN_DOMAIN") or os.environ.get("ATLASSIAN_DOMAIN")
        # 项目键从环境读取（无公司默认值；未配则退回类属性占位）
        self.PROJECT_KEY = env.get("JIRA_PROJECT") or os.environ.get("JIRA_PROJECT") or self.PROJECT_KEY

        if not all([self.username, self.api_key, self.domain]):
            print("Error: 认证信息未设置。")
            print(f"请在 {SKILL_DIR}/.env 中设置以下内容:")
            print("  ATLASSIAN_USERNAME=your-email@example.com")
            print("  ATLASSIAN_API_KEY=your-api-token")
            print("  ATLASSIAN_DOMAIN=your-domain.atlassian.net")
            print(f"\n模板文件: {SKILL_DIR}/.env.example")
            sys.exit(1)

        self.base_url = f"https://{self.domain}/rest/api/3"
        self.auth = base64.b64encode(f"{self.username}:{self.api_key}".encode()).decode()
        self._current_account_id: Optional[str] = None
        self._account_id_fetched: bool = False

    @property
    def current_account_id(self) -> Optional[str]:
        """懒加载当前认证用户的 accountId（仅在创建工单时调用）"""
        if not self._account_id_fetched:
            self._account_id_fetched = True
            try:
                result = self._request("GET", "/myself", exit_on_error=False)
                self._current_account_id = result.get("accountId")
            except (urllib.error.URLError, urllib.error.HTTPError):
                self._current_account_id = None
        return self._current_account_id

    def _request(self, method: str, endpoint: str, data: Optional[dict] = None,
                 exit_on_error: bool = True) -> Union[dict, list]:
        """向 JIRA API 发送 HTTP 请求"""
        url = f"{self.base_url}{endpoint}"
        headers = {
            "Authorization": f"Basic {self.auth}",
            "Content-Type": "application/json",
            "Accept": "application/json"
        }

        body = json.dumps(data).encode() if data else None
        req = urllib.request.Request(url, data=body, headers=headers, method=method)

        try:
            with urllib.request.urlopen(req, timeout=30) as response:
                if response.status == 204:  # No Content
                    return {"status": "success"}
                return json.loads(response.read().decode())
        except urllib.error.HTTPError as e:
            if not exit_on_error:
                raise
            error_body = e.read().decode() if e.fp else ""
            print(f"Error {e.code}: {e.reason}")
            if error_body:
                try:
                    print(json.dumps(json.loads(error_body), indent=2, ensure_ascii=False))
                except (json.JSONDecodeError, ValueError):
                    print(error_body)
            sys.exit(1)
        except urllib.error.URLError as e:
            if not exit_on_error:
                raise
            print(f"Error: 网络请求失败 - {e.reason}")
            sys.exit(1)

    def get(self, ticket_key: str) -> dict:
        """获取工单信息"""
        result = self._request("GET", f"/issue/{ticket_key}")
        fields = result["fields"]
        return {
            "key": result["key"],
            "summary": fields["summary"],
            "status": fields["status"]["name"],
            "issuetype": fields["issuetype"]["name"],
            "assignee": fields["assignee"]["displayName"] if fields["assignee"] else None,
            "description": fields["description"],
            "duedate": fields.get("duedate"),
            "labels": fields.get("labels", []),
            "parent": fields.get("parent", {}).get("key"),
            "subtasks": [
                {"key": st["key"], "summary": st["fields"]["summary"], "status": st["fields"]["status"]["name"]}
                for st in fields.get("subtasks", [])
            ],
            # 评论/回复：常含关键澄清、追加需求、决策——调查时必读，勿只看 description。
            # body 为 ADF（Atlassian Document Format）JSON，与 description 同样需递归解析取文本+链接。
            "comments": [
                {
                    "author": (c.get("author") or {}).get("displayName"),
                    "created": c.get("created"),
                    "body": c.get("body"),
                }
                for c in (fields.get("comment") or {}).get("comments", [])
            ],
        }

    @staticmethod
    def _normalize_jql(jql: str) -> str:
        """规范化 JQL 查询（/search/jql 端点不支持 !=，转换为 'not in'）"""
        import re
        return re.sub(r'(\w+)\s*\\?!=\s*(\"[^\"]*\"|\S+)', r'\1 not in (\2)', jql)

    def search(self, jql: str, max_results: int = 50) -> list:
        """使用 JQL 搜索工单"""
        result = self._request("POST", "/search/jql", {
            "jql": self._normalize_jql(jql),
            "maxResults": max_results,
            "fields": ["summary", "status", "assignee", "issuetype", "duedate", "labels", "parent"],
        })
        return [
            {
                "key": issue["key"],
                "summary": issue["fields"]["summary"],
                "status": issue["fields"]["status"]["name"],
                "issuetype": issue["fields"]["issuetype"]["name"],
                "assignee": issue["fields"]["assignee"]["displayName"] if issue["fields"].get("assignee") else None,
                "duedate": issue["fields"].get("duedate"),
                "labels": issue["fields"].get("labels", []),
                "parent": issue["fields"].get("parent", {}).get("key") if issue["fields"].get("parent") else None,
            }
            for issue in result.get("issues", [])
        ]

    # 工单类型 ID / 自定义字段均为「JIRA 实例特有」示例值，运行时 __init__ 会用
    # 环境变量 JIRA_PROJECT 覆盖 PROJECT_KEY；创建工单前请按你的实例
    # /rest/api/3/issue/createmeta/<PROJECT>/issuetypes 校对下列 ID。
    PROJECT_KEY = ""  # 占位：由环境变量 JIRA_PROJECT 提供
    ISSUE_TYPE_IDS = {
        "长篇故事": "10000",     # Epic（示例 ID，按实例调整）
        "故事": "10006",         # Story
        "任务": "10007",         # Task
        "子任务": "10008",       # Subtask
        "缺陷": "10009",        # Bug
    }

    # 默认字段值（示例，按你的 JIRA 实例调整；所有新建工单自动设置）
    DEFAULT_TEAM_ID = ""  # 团队字段值（实例特有 UUID）
    TEAM_FIELD = "customfield_10001"
    DEFAULT_SYSTEM = {"id": "10022"}  # 系统字段值（示例）
    SYSTEM_FIELD = "customfield_10037"

    def _resolve_duedate(self, duedate: Optional[str]) -> Optional[str]:
        """解析截止日期。未指定时返回 None（由调用方处理继承逻辑）"""
        return duedate

    def _build_time_tracking(self, estimate_hours: float, default_hours: int) -> dict:
        """构建工时跟踪字段"""
        hours = int(estimate_hours) if estimate_hours > 0 else default_hours
        return {
            "originalEstimate": f"{hours}h",
            "remainingEstimate": f"{hours}h"
        }

    def _get_latest_server_version(self) -> Optional[dict]:
        """获取最新的未发布 Server 版本"""
        versions = self._request("GET", f"/project/{self.PROJECT_KEY}/versions")
        server_versions = [
            v for v in versions
            if v["name"].startswith("Server") and not v.get("released", False)
        ]
        return {"id": server_versions[-1]["id"]} if server_versions else None

    def create_task(self, summary: str, description: str = "",
                    estimate_hours: float = 0, duedate: str = None,
                    labels: list = None, issue_type: str = "任务") -> dict:
        """创建独立工单（非子工单）

        Args:
            summary: 工单标题
            description: 工单描述（markdown 格式）
            estimate_hours: 预估工时（小时），默认 8h
            duedate: 截止日期（YYYY-MM-DD 格式）
            labels: 标签列表
            issue_type: 工单类型（"任务"、"故事"、"缺陷"），默认 "任务"
        """
        if issue_type not in self.ISSUE_TYPE_IDS:
            raise ValueError(f"不支持的工单类型 '{issue_type}'。可用: {list(self.ISSUE_TYPE_IDS.keys())}")

        adf_description = self._build_adf_description(description)
        fix_version = self._get_latest_server_version()

        data = {
            "fields": {
                "project": {"key": self.PROJECT_KEY},
                "summary": summary,
                "issuetype": {"id": self.ISSUE_TYPE_IDS[issue_type]},
                "description": adf_description,
                "timetracking": self._build_time_tracking(estimate_hours, 8),
            }
        }
        # 实例特有字段：仅在配置了值时附加（未配则由 JIRA 用项目默认）
        if self.DEFAULT_TEAM_ID:
            data["fields"][self.TEAM_FIELD] = self.DEFAULT_TEAM_ID
        if self.DEFAULT_SYSTEM:
            data["fields"][self.SYSTEM_FIELD] = self.DEFAULT_SYSTEM

        if self.current_account_id:
            data["fields"]["assignee"] = {"accountId": self.current_account_id}
        if fix_version:
            data["fields"]["fixVersions"] = [fix_version]
        if duedate:
            data["fields"]["duedate"] = duedate
        if labels:
            data["fields"]["labels"] = labels

        result = self._request("POST", "/issue", data)
        return {"key": result["key"], "self": result["self"]}

    def create_subtask(self, parent_key: str, summary: str, description: str = "",
                       estimate_hours: float = 0, duedate: str = None, labels: list = None,
                       issue_type: str = None, inherit_labels: bool = True) -> dict:
        """在父工单下创建子工单

        Args:
            parent_key: 父工单 key
            summary: 工单标题
            description: 工单描述（markdown 格式）
            estimate_hours: 预估工时（小时）
            duedate: 截止日期（YYYY-MM-DD 格式）
            labels: 标签列表（为 None 且 inherit_labels=True 时继承父工单标签）
            issue_type: Epic 子工单类型（"任务" 或 "故事"），为 None 时自动判断
            inherit_labels: 为 True 且 labels 为 None 时，继承父工单标签
        """
        # 获取父工单以确定项目和工单类型
        parent = self._request("GET", f"/issue/{parent_key}")
        project_key = parent["fields"]["project"]["key"]
        parent_type = parent["fields"]["issuetype"]["name"]

        # 验证项目 key
        if project_key != self.PROJECT_KEY:
            raise ValueError(
                f"不支持的项目 '{project_key}'。"
                f"ISSUE_TYPE_IDS 仅配置了 '{self.PROJECT_KEY}' 项目。"
            )

        # 未指定标签时从父工单继承
        if labels is None and inherit_labels:
            parent_labels = parent["fields"].get("labels", [])
            if parent_labels:
                labels = parent_labels

        # 未指定截止日期时从父工单继承
        if duedate is None:
            parent_duedate = parent["fields"].get("duedate")
            if parent_duedate:
                duedate = parent_duedate

        # 构建 ADF 格式描述
        adf_description = self._build_adf_description(description)

        # 根据父工单类型决定子工单类型和默认预估
        if parent_type == "长篇故事":
            child_type = issue_type if issue_type in ["任务", "故事"] else "任务"
            child_type_id = self.ISSUE_TYPE_IDS[child_type]
            default_estimate_hours = 8
        else:
            child_type_id = self.ISSUE_TYPE_IDS["子任务"]
            default_estimate_hours = 4

        resolved_duedate = self._resolve_duedate(duedate)
        data = {
            "fields": {
                "project": {"key": project_key},
                "parent": {"key": parent_key},
                "summary": summary,
                "issuetype": {"id": child_type_id},
                "description": adf_description,
                "timetracking": self._build_time_tracking(estimate_hours, default_estimate_hours),
            }
        }
        # 实例特有字段：仅在配置了值时附加
        if self.DEFAULT_TEAM_ID:
            data["fields"][self.TEAM_FIELD] = self.DEFAULT_TEAM_ID
        if self.DEFAULT_SYSTEM:
            data["fields"][self.SYSTEM_FIELD] = self.DEFAULT_SYSTEM

        if self.current_account_id:
            data["fields"]["assignee"] = {"accountId": self.current_account_id}

        fix_version = self._get_latest_server_version()
        if fix_version:
            data["fields"]["fixVersions"] = [fix_version]

        if resolved_duedate:
            data["fields"]["duedate"] = resolved_duedate

        if labels:
            data["fields"]["labels"] = labels

        result = self._request("POST", "/issue", data)
        return {"key": result["key"], "self": result["self"]}

    def _build_adf_description(self, description: str) -> dict:
        """将纯文本转换为 ADF（Atlassian Document Format）格式描述"""
        if not description:
            return {
                "type": "doc",
                "version": 1,
                "content": [{"type": "paragraph", "content": []}]
            }

        # 将类 Markdown 语法解析为 ADF
        lines = description.split('\n')
        content = []
        current_list = None
        current_list_type = None

        for line in lines:
            stripped = line.strip()

            # 跳过空行
            if not stripped:
                if current_list:
                    content.append(current_list)
                    current_list = None
                    current_list_type = None
                continue

            # 标题（## 二级, ### 三级）
            if stripped.startswith('## ') or stripped.startswith('### '):
                if current_list:
                    content.append(current_list)
                    current_list = None
                    current_list_type = None
                if stripped.startswith('### '):
                    level = 3
                    text = stripped[4:]
                else:
                    level = 2
                    text = stripped[3:]
                content.append({
                    "type": "heading",
                    "attrs": {"level": level},
                    "content": [{"type": "text", "text": text}]
                })
            # 有序列表项
            elif stripped[0].isdigit() and '. ' in stripped[:4]:
                if current_list_type != "orderedList":
                    if current_list:
                        content.append(current_list)
                    current_list = {"type": "orderedList", "attrs": {"order": 1}, "content": []}
                    current_list_type = "orderedList"
                text = stripped.split('. ', 1)[1] if '. ' in stripped else stripped
                current_list["content"].append({
                    "type": "listItem",
                    "content": [{"type": "paragraph", "content": [{"type": "text", "text": text}]}]
                })
            # 无序列表项（- 或 *）
            elif stripped.startswith('- ') or stripped.startswith('* '):
                if current_list_type != "bulletList":
                    if current_list:
                        content.append(current_list)
                    current_list = {"type": "bulletList", "content": []}
                    current_list_type = "bulletList"
                current_list["content"].append({
                    "type": "listItem",
                    "content": [{"type": "paragraph", "content": [{"type": "text", "text": stripped[2:]}]}]
                })
            # 普通段落
            else:
                if current_list:
                    content.append(current_list)
                    current_list = None
                    current_list_type = None
                content.append({
                    "type": "paragraph",
                    "content": [{"type": "text", "text": stripped}]
                })

        # 添加剩余列表
        if current_list:
            content.append(current_list)

        # 无内容时添加空段落
        if not content:
            content.append({"type": "paragraph", "content": []})

        return {"type": "doc", "version": 1, "content": content}

    def bulk_create_subtasks(self, parent_key: str, subtasks: list, issue_type: str = None) -> list:
        """批量创建子工单

        Args:
            parent_key: 父工单 key
            subtasks: 字典列表，键: summary, description（可选）, estimate_hours（可选）, issue_type（可选）
            issue_type: 所有子工单的默认工单类型（可在单个工单中覆盖）
        """
        results = []
        for st in subtasks:
            result = self.create_subtask(
                parent_key=parent_key,
                summary=st["summary"],
                description=st.get("description", ""),
                estimate_hours=st.get("estimate_hours", 0),
                duedate=st.get("duedate"),
                labels=st.get("labels"),
                issue_type=st.get("issue_type", issue_type)
            )
            results.append(result)
            print(f"已创建: {result['key']} - {st['summary']}")
        return results

    def update(self, ticket_key: str, fields: dict) -> dict:
        """更新工单字段

        支持的字段:
          - summary: str（标题）
          - description: str（描述，自动转换为 ADF 格式）
          - assignee: str（账户 ID）或 None（取消分配）
          - duedate: str（YYYY-MM-DD 格式）
          - labels: list[str]（标签列表）
        """
        update_data = {"fields": {}}

        for key, value in fields.items():
            if key == "description" and isinstance(value, str):
                # 将纯文本转换为 ADF 格式
                update_data["fields"]["description"] = self._build_adf_description(value)
            elif key == "assignee":
                update_data["fields"]["assignee"] = {"accountId": value} if value else None
            else:
                update_data["fields"][key] = value

        self._request("PUT", f"/issue/{ticket_key}", update_data)
        return {"status": "success", "key": ticket_key}

    def delete(self, ticket_key: str) -> dict:
        """删除工单"""
        self._request("DELETE", f"/issue/{ticket_key}")
        return {"status": "deleted", "key": ticket_key}

    def transition(self, ticket_key: str, transition_name: str) -> dict:
        """变更工单状态"""
        # 获取可用的状态转换
        transitions = self._request("GET", f"/issue/{ticket_key}/transitions")

        target = None
        for t in transitions["transitions"]:
            if t["name"].lower() == transition_name.lower():
                target = t
                break

        if not target:
            available = [t["name"] for t in transitions["transitions"]]
            print(f"Error: 未找到状态转换 '{transition_name}'")
            print(f"可用的状态转换: {available}")
            sys.exit(1)

        self._request("POST", f"/issue/{ticket_key}/transitions", {"transition": {"id": target["id"]}})
        return {"status": "success", "key": ticket_key, "transition": transition_name}


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(1)

    api = JiraAPI()
    command = sys.argv[1]

    if command == "get":
        if len(sys.argv) < 3:
            print("用法: jira_api.py get <工单key>")
            sys.exit(1)
        result = api.get(sys.argv[2])
        print(json.dumps(result, indent=2, ensure_ascii=False))

    elif command == "create-task":
        if len(sys.argv) < 3:
            print("用法: jira_api.py create-task <标题> [描述] [预估工时] [工单类型]")
            sys.exit(1)
        result = api.create_task(
            summary=sys.argv[2],
            description=sys.argv[3] if len(sys.argv) > 3 else "",
            estimate_hours=float(sys.argv[4]) if len(sys.argv) > 4 else 0,
            issue_type=sys.argv[5] if len(sys.argv) > 5 else "任务"
        )
        print(json.dumps(result, indent=2, ensure_ascii=False))

    elif command == "create":
        if len(sys.argv) < 4:
            print("用法: jira_api.py create <父工单key> <标题> [描述] [预估工时] [工单类型]")
            sys.exit(1)
        result = api.create_subtask(
            parent_key=sys.argv[2],
            summary=sys.argv[3],
            description=sys.argv[4] if len(sys.argv) > 4 else "",
            estimate_hours=float(sys.argv[5]) if len(sys.argv) > 5 else 0,
            issue_type=sys.argv[6] if len(sys.argv) > 6 else None
        )
        print(json.dumps(result, indent=2, ensure_ascii=False))

    elif command == "bulk-create":
        if len(sys.argv) < 4:
            print("用法: jira_api.py bulk-create <父工单key> '<json数组>'")
            print('示例: jira_api.py bulk-create PROJ-1234 \'[{"summary": "任务1"}, {"summary": "任务2"}]\'')
            sys.exit(1)
        subtasks = json.loads(sys.argv[3])
        results = api.bulk_create_subtasks(sys.argv[2], subtasks)
        print(json.dumps(results, indent=2, ensure_ascii=False))

    elif command == "update":
        if len(sys.argv) < 4:
            print("用法: jira_api.py update <工单key> '<json字段>'")
            print('示例: jira_api.py update PROJ-1234 \'{"summary": "新标题"}\'')
            sys.exit(1)
        fields = json.loads(sys.argv[3])
        result = api.update(sys.argv[2], fields)
        print(json.dumps(result, indent=2, ensure_ascii=False))

    elif command == "delete":
        if len(sys.argv) < 3:
            print("用法: jira_api.py delete <工单key>")
            sys.exit(1)
        result = api.delete(sys.argv[2])
        print(json.dumps(result, indent=2, ensure_ascii=False))

    elif command == "transition":
        if len(sys.argv) < 4:
            print("用法: jira_api.py transition <工单key> <状态名称>")
            sys.exit(1)
        result = api.transition(sys.argv[2], sys.argv[3])
        print(json.dumps(result, indent=2, ensure_ascii=False))

    elif command == "search":
        if len(sys.argv) < 3:
            print("用法: jira_api.py search '<JQL 查询>'")
            print('示例: jira_api.py search \'project = PROJ AND parent = PROJ-1234\'')
            sys.exit(1)
        max_results = int(sys.argv[3]) if len(sys.argv) > 3 else 50
        results = api.search(sys.argv[2], max_results)
        print(json.dumps(results, indent=2, ensure_ascii=False))

    else:
        print(f"未知命令: {command}")
        print(__doc__)
        sys.exit(1)


if __name__ == "__main__":
    main()
