#!/usr/bin/env python3
"""
Linear GraphQL API 操作脚本（对标 jira_api.py，供接收器路由与 jira-to-issue skill 读取工单）

使用方法:
  python linear_api.py <command> [options]

命令:
  get <identifier>                     获取工单（如 ENG-123）
  comment <identifier> <body>          给工单加评论（Markdown 正文）

环境变量（从同目录 .env 读取）:
  LINEAR_API_KEY      Personal API Key（Settings → API → Personal API keys）

说明:
  - Linear 工单号形如 TEAM-数字（团队 key + 序号），如 ENG-123。
  - 返回字段与 jira_api.py get 同构：key/summary/status/issuetype/assignee/
    description/labels/parent/subtasks，外加 Linear 的 url。
  - description 是 Markdown 纯文本（不是 JIRA 的 ADF JSON），下游可直接用。
"""

import json
import os
import sys
from pathlib import Path
from typing import Optional
import urllib.request
import urllib.error


# ai-workflow 工具集自包含：.env 与本脚本同目录
DEFAULT_ENV_PATH = Path(__file__).resolve().parent / ".env"
ENDPOINT = "https://api.linear.app/graphql"


def load_env(env_path: str = None) -> dict:
    """从 .env 文件加载环境变量（与 jira_api.py 同款解析，去成对引号）"""
    path = Path(env_path) if env_path else DEFAULT_ENV_PATH
    env = {}
    if path.exists():
        with open(path, encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if line and not line.startswith("#") and "=" in line:
                    key, value = line.split("=", 1)
                    value = value.strip()
                    if len(value) >= 2 and value[0] == value[-1] and value[0] in ("'", '"'):
                        value = value[1:-1]
                    env[key.strip()] = value
    return env


class LinearAPI:
    def __init__(self):
        env = load_env()
        self.api_key = env.get("LINEAR_API_KEY") or os.environ.get("LINEAR_API_KEY")

        if not self.api_key:
            print("Error: 认证信息未设置。")
            print(f"请在 {DEFAULT_ENV_PATH} 中设置:")
            print("  LINEAR_API_KEY=lin_api_xxx")
            print("\n获取: Linear → Settings → API → Personal API keys")
            sys.exit(1)

    def _request(self, query: str, variables: dict, exit_on_error: bool = True) -> dict:
        """向 Linear GraphQL 端点发请求。Personal API Key 直接作 Authorization 值（无 Bearer）。"""
        body = json.dumps({"query": query, "variables": variables}).encode()
        headers = {
            "Authorization": self.api_key,
            "Content-Type": "application/json",
        }
        req = urllib.request.Request(ENDPOINT, data=body, headers=headers, method="POST")
        try:
            with urllib.request.urlopen(req, timeout=30) as response:
                payload = json.loads(response.read().decode())
        except urllib.error.HTTPError as e:
            if not exit_on_error:
                raise
            error_body = e.read().decode() if e.fp else ""
            print(f"Error {e.code}: {e.reason}")
            if error_body:
                print(error_body)
            sys.exit(1)
        except urllib.error.URLError as e:
            if not exit_on_error:
                raise
            print(f"Error: 网络请求失败 - {e.reason}")
            sys.exit(1)

        # GraphQL 业务错误在 200 里
        if payload.get("errors"):
            if not exit_on_error:
                raise RuntimeError(json.dumps(payload["errors"], ensure_ascii=False))
            print("Error: GraphQL 返回错误")
            print(json.dumps(payload["errors"], indent=2, ensure_ascii=False))
            sys.exit(1)
        return payload.get("data", {})

    @staticmethod
    def _split_identifier(identifier: str) -> tuple:
        """ENG-123 → ('ENG', 123)。最后一个 '-' 之前为团队 key。"""
        if "-" not in identifier:
            raise ValueError(f"非法 Linear 工单号 '{identifier}'，应形如 TEAM-数字")
        team, num = identifier.rsplit("-", 1)
        if not num.isdigit():
            raise ValueError(f"非法 Linear 工单号 '{identifier}'，序号非数字")
        return team, int(num)

    _ISSUE_FIELDS = """
      id identifier title description url
      state { name type }
      assignee { displayName }
      parent { identifier }
      labels { nodes { name } }
      children { nodes { identifier title state { name } } }
    """

    def _fetch_node(self, identifier: str) -> Optional[dict]:
        """按 team key + number 过滤取单条 issue 节点（GraphQL 无按 identifier 直查）。"""
        team, number = self._split_identifier(identifier)
        query = """
        query($team: String!, $number: Float!) {
          issues(filter: {team: {key: {eq: $team}}, number: {eq: $number}}, first: 1) {
            nodes { %s }
          }
        }
        """ % self._ISSUE_FIELDS
        data = self._request(query, {"team": team, "number": float(number)})
        nodes = data.get("issues", {}).get("nodes", [])
        return nodes[0] if nodes else None

    @staticmethod
    def _normalize(node: dict) -> dict:
        """GraphQL 节点 → 与 jira_api.py get 同构的字典。"""
        state = node.get("state") or {}
        assignee = node.get("assignee") or {}
        parent = node.get("parent") or {}
        return {
            "key": node.get("identifier"),
            "summary": node.get("title"),
            "status": state.get("name"),
            "issuetype": state.get("type"),  # Linear 无 issue type，用状态类别（如 backlog/started/done）
            "assignee": assignee.get("displayName"),
            "description": node.get("description") or "",  # Markdown 纯文本
            "labels": [l["name"] for l in (node.get("labels") or {}).get("nodes", [])],
            "parent": parent.get("identifier"),
            "subtasks": [
                {"key": c["identifier"], "summary": c["title"],
                 "status": (c.get("state") or {}).get("name")}
                for c in (node.get("children") or {}).get("nodes", [])
            ],
            "url": node.get("url"),
        }

    def get(self, identifier: str) -> dict:
        """获取工单信息（同构于 jira_api.py get）"""
        node = self._fetch_node(identifier)
        if not node:
            print(f"Error: 未找到工单 {identifier}")
            sys.exit(1)
        return self._normalize(node)

    def comment(self, identifier: str, body: str) -> dict:
        """给工单加评论（Markdown）。需先取 issue 的 UUID。"""
        node = self._fetch_node(identifier)
        if not node:
            print(f"Error: 未找到工单 {identifier}")
            sys.exit(1)
        mutation = """
        mutation($issueId: String!, $body: String!) {
          commentCreate(input: {issueId: $issueId, body: $body}) {
            success
            comment { id url }
          }
        }
        """
        data = self._request(mutation, {"issueId": node["id"], "body": body})
        result = data.get("commentCreate", {})
        return {"status": "success" if result.get("success") else "failed",
                "comment": result.get("comment")}


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(1)

    api = LinearAPI()
    command = sys.argv[1]

    if command == "get":
        if len(sys.argv) < 3:
            print("用法: linear_api.py get <工单号>")
            sys.exit(1)
        result = api.get(sys.argv[2])
        print(json.dumps(result, indent=2, ensure_ascii=False))

    elif command == "comment":
        if len(sys.argv) < 4:
            print("用法: linear_api.py comment <工单号> <正文>")
            sys.exit(1)
        result = api.comment(sys.argv[2], sys.argv[3])
        print(json.dumps(result, indent=2, ensure_ascii=False))

    else:
        print(f"未知命令: {command}")
        print(__doc__)
        sys.exit(1)


if __name__ == "__main__":
    main()
