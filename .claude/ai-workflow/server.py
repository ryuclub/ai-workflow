#!/usr/bin/env python3
"""单例 webhook 接收器（多仓路由）：JIRA/Linear/GitHub 事件 → 目标仓 worktree 里跑 claude -p。

路由：
- GitHub(C)：payload repository.full_name → config.repos 反查 → 该仓 path。
- JIRA(B)：读票 summary（标题）按 config 各仓 match 关键词判定；1 命中→该仓，0→default，≥2→冲突 Slack 不派。
- Linear(B)：issue 打触发标签 → 读票标题，同 JIRA 走 resolve_by_title 路由。
配置见同目录 config.json（含本地路径 → gitignore）。
"""
import hashlib
import hmac
import json
import os
import re
import shutil
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

DIR = Path(__file__).resolve().parent


def load_env():
    env = {}
    p = DIR / ".env"
    if p.exists():
        for line in p.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, v = line.split("=", 1)
                v = v.strip()
                if len(v) >= 2 and v[0] == v[-1] and v[0] in ("'", '"'):
                    v = v[1:-1]
                env[k.strip()] = v
    return env


ENV = load_env()
CLAUDE_BIN = ENV.get("CLAUDE_BIN", "claude")
WORKTREE_BASE = ENV.get("WORKTREE_BASE", "/tmp/wf-worktrees")
APPROVED_LABEL = ENV.get("APPROVED_LABEL", "已审核")
JIRA_TRIGGER_STATUS = ENV.get("JIRA_TRIGGER_STATUS", "待AI处理")
# 工单号格式校验（与 Linear 对称，不写死某项目前缀）。默认接受任意项目键 ABC-123；
# 如需收窄到单项目，设 JIRA_ID_PATTERN=^PROJ-\d+$ 之类。
JIRA_ID_PATTERN = ENV.get("JIRA_ID_PATTERN", r"^[A-Z][A-Z0-9]+-\d+$")
GITHUB_WEBHOOK_SECRET = ENV.get("GITHUB_WEBHOOK_SECRET", "")
JIRA_WEBHOOK_TOKEN = ENV.get("JIRA_WEBHOOK_TOKEN", "")
LINEAR_WEBHOOK_SECRET = ENV.get("LINEAR_WEBHOOK_SECRET", "")
LINEAR_TRIGGER_LABEL = ENV.get("LINEAR_TRIGGER_LABEL", "AI处理")
LINEAR_ID_PATTERN = ENV.get("LINEAR_ID_PATTERN", r"^[A-Z][A-Z0-9]+-\d+$")
PORT = int(ENV.get("PORT", "8787"))


def load_config():
    p = DIR / "config.json"
    if p.exists():
        try:
            return json.loads(p.read_text(encoding="utf-8"))
        except Exception as e:
            print(f"[配置] config.json 解析失败: {e}", flush=True)
    return {"default": None, "repos": {}}


CONFIG = load_config()
REPOS = CONFIG.get("repos", {})
DEFAULT_REPO = CONFIG.get("default")

SEEN_TTL = int(ENV.get("SEEN_TTL", "300"))  # 去重仅为防抖（同一事件秒级重投）；超 TTL 视为「重新触发」放行
_lock = threading.Lock()
_seen = {}  # key -> 首次见到的时间戳


def log(m):
    print(f"[{time.strftime('%Y-%m-%d %H:%M:%S')}] {m}", flush=True)


def slack(m):
    s = DIR / "notify.sh"
    if s.exists():
        try:
            subprocess.run(["bash", str(s), f"{m}  ·{time.strftime('%m-%d %H:%M')}"],
                           timeout=20, check=False)
        except Exception as e:
            log(f"slack 失败: {e}")


_alert_lock = threading.Lock()
_alert_last = {}  # 告警类别 -> 上次发送时戳


def slack_throttled(cat, m):
    """节流告警：同一类别（cat）SEEN_TTL 内只发一次 Slack，防扫描/重试风暴刷屏。
    始终落 log，确保不丢线索。"""
    log(m)
    now = time.time()
    with _alert_lock:
        last = _alert_last.get(cat)
        if last is not None and now - last < SEEN_TTL:
            return
        _alert_last[cat] = now
    slack(m)


def repo_entry(name):
    e = REPOS.get(name)
    if not e:
        return None
    return {"name": name, "github": e.get("github", ""),
            "path": e.get("path", ""), "match": e.get("match", [])}


def resolve_by_github(full_name):
    for name, e in REPOS.items():
        if e.get("github") == full_name:
            return repo_entry(name)
    return None


def resolve_by_title(summary):
    """标题判定目标仓（方案1：显式标记优先 + 关键词兜底）。返回 (repo|None, reason)。
    1) 标题含 `[<repo-key>]` 显式标记 → 铁定按它（多个不同标记=冲突）。
    2) 否则按各仓 match 关键词子串匹配（1命中→该仓，≥2→冲突）。
    3) 否则 default。"""
    s = (summary or "").lower()
    # 1) 显式标记优先
    explicit = [name for name in REPOS if f"[{name}]".lower() in s]
    if len(explicit) == 1:
        return repo_entry(explicit[0]), "explicit:" + explicit[0]
    if len(explicit) >= 2:
        return None, "conflict-explicit:" + ",".join(explicit)
    # 2) 关键词兜底
    hits = [name for name, e in REPOS.items()
            if any(kw.lower() in s for kw in e.get("match", []))]
    if len(hits) == 1:
        return repo_entry(hits[0]), "keyword:" + hits[0]
    if len(hits) >= 2:
        return None, "conflict-keyword:" + ",".join(hits)
    # 3) 默认仓
    return (repo_entry(DEFAULT_REPO), "default") if DEFAULT_REPO else (None, "no-default")


def jira_get(key):
    """receiver 用自带 jira_api 读票（取 summary 做路由）。"""
    try:
        r = subprocess.run(["python3", str(DIR / "jira_api.py"), "get", key],
                           capture_output=True, text=True, timeout=30)
        return json.loads(r.stdout)
    except Exception as e:
        log(f"jira_get 失败 {key}: {e}")
        return {}


def linear_get(key):
    """receiver 用自带 linear_api 读票（取 summary + 权威 labels 做触发判定/路由）。"""
    try:
        r = subprocess.run(["python3", str(DIR / "linear_api.py"), "get", key],
                           capture_output=True, text=True, timeout=30)
        return json.loads(r.stdout)
    except Exception as e:
        log(f"linear_get 失败 {key}: {e}")
        return {}


def detect_base(repo_path):
    """目标仓默认分支（worktree 检出基；C 内部再据 branch.md 改 PR base）。"""
    r = subprocess.run(["git", "-C", repo_path, "symbolic-ref", "refs/remotes/origin/HEAD"],
                       capture_output=True, text=True)
    ref = r.stdout.strip()
    return ref.rsplit("/", 1)[-1] if ref else "main"


def check_c_outcome(num, repo):
    """C 跑完后的权威裁决：未标『已实装』即视为未完成 → 转『待裁决』+ Slack（不评论 Issue）。"""
    rn = repo["name"]
    try:
        r = subprocess.run(["gh", "issue", "view", str(num), "-R", repo["github"],
                            "--json", "labels", "--jq", "[.labels[].name]|join(\",\")"],
                           capture_output=True, text=True)
        labels = r.stdout.strip()
    except Exception as e:
        slack(f"⚠️ [{rn}] Issue #{num} 裁决检查异常：{e}")
        return
    if "已实装" in labels:
        slack(f"✅ [{rn}] Issue #{num} 实装完成（已标『已实装』）")
        return
    log(f"[{rn}][C-{num}] 未完成（未标已实装）→ 待裁决")
    subprocess.run(["gh", "issue", "edit", str(num), "-R", repo["github"],
                    "--add-label", "待裁决", "--remove-label", "已审核"],
                   capture_output=True, text=True)
    slack(f"⚖️ [{rn}] Issue #{num} 自动实装未完成 → 转『待裁决』，需人工裁决"
          "（可能：方案未定 / 前置依赖未满足 / 构建·测试失败超限）")


def seen(i):
    """带 TTL 的去重（防抖）：TTL 内重复 → True 丢弃；超 TTL 或首见 → False 放行并刷新时戳。"""
    if not i:
        return False
    now = time.time()
    with _lock:
        ts = _seen.get(i)
        if ts is not None and now - ts < SEEN_TTL:
            return True
        # 顺带清理过期项，避免 _seen 无界增长
        for k in [k for k, t in _seen.items() if now - t >= SEEN_TTL]:
            del _seen[k]
        _seen[i] = now
        return False


def run_task(prompt, label, repo):
    rn, rp = repo["name"], repo["path"]
    if not rp or not os.path.isdir(rp):
        slack(f"⚠️ [{rn}] 仓路径无效：{rp}（检查 config.json）")
        return
    wt = os.path.join(WORKTREE_BASE, f"{rn}-{label}-{int(time.time())}")
    os.makedirs(WORKTREE_BASE, exist_ok=True)
    base = detect_base(rp)
    try:
        subprocess.run(["git", "-C", rp, "worktree", "add", "--detach", wt, base],
                       check=True, capture_output=True, text=True)
        # worktree 是干净检出，把工具集合并 .env（host 侧凭据）注入进去
        dst = os.path.join(wt, ".claude/ai-workflow/.env")
        if os.path.exists(DIR / ".env"):
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            shutil.copy2(DIR / ".env", dst)
        log(f"[{rn}][{label}] 跑（base={base}）: {prompt}")
        p = subprocess.run([CLAUDE_BIN, "-p", prompt, "--dangerously-skip-permissions"],
                           cwd=wt, capture_output=True, text=True, timeout=3600)
        # claude 未登录时会打印「Not logged in」却仍以退出码 0 退出 —— 必须显式识别，
        # 否则后台服务环境（launchd/systemd 够不到 keychain 登录态）会静默「假成功」：
        # 只发「🏁 结束」却没建出任何 Issue/PR。（2026-06-18 实测踩坑）
        out = (p.stdout or "") + (p.stderr or "")
        auth_fail = any(s in out for s in ("Not logged in", "Please run /login", "Invalid API key"))
        if p.returncode or auth_fail:
            reason = ("claude 未登录/认证失效——接收器须在能访问 keychain 的登录会话内跑，"
                      "或配 CLAUDE_CODE_OAUTH_TOKEN/ANTHROPIC_API_KEY") if auth_fail else f"退出码 {p.returncode}"
            slack(f"⚠️ [{rn}] 失败[{label}]（{reason}）: {prompt}\n{out[-300:]}")
        else:
            log(f"[{rn}][{label}] 完成")
            if label.startswith("B-"):
                slack(f"🏁 [{rn}] [{label}] 结束")
    except Exception as e:
        log(f"[{rn}][{label}] 错误: {e}")
        slack(f"⚠️ [{rn}] 任务[{label}] 异常: {e}")
    finally:
        subprocess.run(["git", "-C", rp, "worktree", "remove", "--force", wt],
                       capture_output=True, text=True)
    if label.startswith("C-"):
        check_c_outcome(label.split("-", 1)[1], repo)


def dispatch(prompt, label, repo):
    threading.Thread(target=run_task, args=(prompt, label, repo), daemon=True).start()


class H(BaseHTTPRequestHandler):
    def _r(self, c, m=""):
        self.send_response(c)
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.end_headers()
        if m:
            self.wfile.write(m.encode())

    def log_message(self, *a):
        pass

    def do_GET(self):
        self._r(200, "ok") if self.path == "/health" else self._r(404)

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(n) if n else b""
        if self.path == "/github":
            return self._gh(body)
        if self.path == "/jira":
            return self._jira(body)
        if self.path == "/linear":
            return self._linear(body)
        self._r(404)

    def _gh(self, body):
        if not GITHUB_WEBHOOK_SECRET:
            slack_throttled("gh-no-secret", "⚠️ GitHub webhook 到达但未配 GITHUB_WEBHOOK_SECRET，全部拒收（.env 缺失）")
            return self._r(500, "no secret")
        exp = "sha256=" + hmac.new(GITHUB_WEBHOOK_SECRET.encode(), body, hashlib.sha256).hexdigest()
        if not hmac.compare_digest(self.headers.get("X-Hub-Signature-256", ""), exp):
            slack_throttled("gh-bad-sig", "⚠️ GitHub 验签失败，webhook 被拒——secret 可能漂移（合法事件正被静默丢弃）")
            return self._r(401, "bad sig")
        if seen(self.headers.get("X-GitHub-Delivery", "")):
            d = self.headers.get("X-GitHub-Delivery", "")
            log(f"GitHub: 重复投递 {d}（{SEEN_TTL}s 内防抖），丢弃")
            slack(f"🔁 GitHub 重复投递 {d}（{SEEN_TTL}s 内防抖，已丢弃）")
            return self._r(200, "dup")
        try:
            pl = json.loads(body)
        except Exception:
            slack_throttled("gh-bad-json", "⚠️ GitHub webhook body 非 JSON，已丢弃")
            return self._r(400)
        if self.headers.get("X-GitHub-Event") == "issues" and pl.get("action") == "labeled":
            name = pl.get("label", {}).get("name", "")
            num = pl.get("issue", {}).get("number")
            full = pl.get("repository", {}).get("full_name", "")
            if name == APPROVED_LABEL and num:
                repo = resolve_by_github(full)
                if not repo:
                    log(f"GitHub: 未登记仓 {full}，忽略")
                    slack(f"⚠️ 收到未登记仓 {full} 的『已审核』(#{num})，config.json 未配，忽略")
                    return self._r(200, "unknown repo")
                log(f"[{repo['name']}] Issue #{num} 已审核 → C")
                slack(f"📥 [{repo['name']}] 收到 Issue #{num} 已审核 → 启动实装 (C)…")
                dispatch(f"/issue-to-pr {num}", f"C-{num}", repo)
                return self._r(202, "C")
        self._r(200, "ignored")

    def _jira(self, body):
        if not JIRA_WEBHOOK_TOKEN:
            slack_throttled("jira-no-token", "⚠️ JIRA webhook 到达但未配 JIRA_WEBHOOK_TOKEN，全部拒收（.env 缺失）")
            return self._r(500, "no token")
        if not hmac.compare_digest(self.headers.get("X-Webhook-Token", ""), JIRA_WEBHOOK_TOKEN):
            slack_throttled("jira-bad-token", "⚠️ JIRA token 校验失败，webhook 被拒——token 可能漂移（合法事件正被静默丢弃）")
            return self._r(401, "bad token")
        log(f"JIRA 收到原始 body: {body[:300].decode('utf-8', 'replace')}")
        try:
            pl = json.loads(body)
        except Exception:
            slack_throttled("jira-bad-json", "⚠️ JIRA webhook body 非 JSON，已丢弃")
            return self._r(400)
        key = pl.get("key") or pl.get("issue", {}).get("key", "")
        status = pl.get("status") or pl.get("issue", {}).get("fields", {}).get("status", {}).get("name", "")
        if seen(f"{key}:{status}"):
            log(f"JIRA: 重复事件 {key}:{status}（{SEEN_TTL}s 内防抖），丢弃")
            slack(f"🔁 JIRA 重复事件 {key}（{status}，{SEEN_TTL}s 内防抖，已丢弃）")
            return self._r(200, "dup")
        if not (key and re.match(JIRA_ID_PATTERN, key) and status == JIRA_TRIGGER_STATUS):
            return self._r(200, "ignored")
        # 读标题路由到目标仓
        summary = jira_get(key).get("summary", "")
        repo, reason = resolve_by_title(summary)
        if reason.startswith("conflict"):
            log(f"{key} 标题路由冲突：{reason}")
            slack(f"⚖️ {key} 标题命中多个仓（{reason}），无法判定路由 → 请人工指定，未派活\n标题：{summary}")
            return self._r(200, "conflict")
        if not repo:
            slack(f"⚠️ {key} 无法路由（无默认仓），config.json 未配 default")
            return self._r(200, "no repo")
        log(f"[{repo['name']}] {key} → B（{reason}）")
        slack(f"📥 [{repo['name']}] 收到 {key}（待AI处理，{reason}）→ 启动整理为 Issue (B)…")
        dispatch(f"/jira-to-issue {key}", f"B-{key}", repo)
        self._r(202, "B")

    def _linear(self, body):
        if not LINEAR_WEBHOOK_SECRET:
            slack_throttled("linear-no-secret", "⚠️ Linear webhook 到达但未配 LINEAR_WEBHOOK_SECRET，全部拒收（.env 缺失）")
            return self._r(500, "no secret")
        # Linear 用 HMAC-SHA256 十六进制（Linear-Signature，无 sha256= 前缀，与 GitHub 略异）
        exp = hmac.new(LINEAR_WEBHOOK_SECRET.encode(), body, hashlib.sha256).hexdigest()
        if not hmac.compare_digest(self.headers.get("Linear-Signature", ""), exp):
            slack_throttled("linear-bad-sig", "⚠️ Linear 验签失败，webhook 被拒——secret 可能漂移（合法事件正被静默丢弃）")
            return self._r(401, "bad sig")
        try:
            pl = json.loads(body)
        except Exception:
            slack_throttled("linear-bad-json", "⚠️ Linear webhook body 非 JSON，已丢弃")
            return self._r(400)
        if pl.get("type") != "Issue":
            return self._r(200, "ignored")
        data = pl.get("data", {}) or {}
        action = pl.get("action", "")
        key = data.get("identifier", "")
        if seen(f"{key}:{data.get('updatedAt') or action}"):
            log(f"Linear: 重复事件 {key}（{SEEN_TTL}s 内防抖），丢弃")
            slack(f"🔁 Linear 重复事件 {key}（{SEEN_TTL}s 内防抖，已丢弃）")
            return self._r(200, "dup")
        # 仅在「新建」或「标签发生变化的更新」事件上继续（无关字段更新不触发，省去无谓 API 读取）
        uf = pl.get("updatedFrom") or {}
        if action == "update" and not any(k in uf for k in ("labelIds", "labels")):
            return self._r(200, "ignored")
        if not (key and re.match(LINEAR_ID_PATTERN, key)):
            return self._r(200, "ignored")
        # webhook payload 不一定展开标签名 → 回查 API 取权威 labels + 标题
        info = linear_get(key)
        if LINEAR_TRIGGER_LABEL not in info.get("labels", []):
            return self._r(200, "ignored")
        summary = info.get("summary", "") or ""
        repo, reason = resolve_by_title(summary)
        if reason.startswith("conflict"):
            log(f"{key} 标题路由冲突：{reason}")
            slack(f"⚖️ {key} 标题命中多个仓（{reason}），无法判定路由 → 请人工指定，未派活\n标题：{summary}")
            return self._r(200, "conflict")
        if not repo:
            slack(f"⚠️ {key} 无法路由（无默认仓），config.json 未配 default")
            return self._r(200, "no repo")
        log(f"[{repo['name']}] {key} → B（linear/{reason}）")
        slack(f"📥 [{repo['name']}] 收到 {key}（Linear『{LINEAR_TRIGGER_LABEL}』，{reason}）→ 启动整理为 Issue (B)…")
        dispatch(f"/jira-to-issue {key}", f"B-{key}", repo)
        self._r(202, "B")


if __name__ == "__main__":
    gh_on = "on" if GITHUB_WEBHOOK_SECRET else "OFF"
    jira_on = "on" if JIRA_WEBHOOK_TOKEN else "OFF"
    linear_on = "on" if LINEAR_WEBHOOK_SECRET else "OFF"
    log(f"接收器 :{PORT}  默认仓={DEFAULT_REPO}  登记仓={list(REPOS)}  "
        f"gh验签={gh_on} jira={jira_on} linear={linear_on}")
    warn = ""
    if not GITHUB_WEBHOOK_SECRET:
        warn += "  ⚠️gh验签OFF(GitHub事件将全拒)"
    if not JIRA_WEBHOOK_TOKEN:
        warn += "  ⚠️jira校验OFF(JIRA事件将全拒)"
    if not LINEAR_WEBHOOK_SECRET:
        warn += "  ⚠️linear验签OFF(Linear事件将全拒)"
    slack(f"🟢 接收器启动 :{PORT}  默认仓={DEFAULT_REPO}  登记仓={list(REPOS)}  "
          f"防抖TTL={SEEN_TTL}s{warn}")
    ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()
