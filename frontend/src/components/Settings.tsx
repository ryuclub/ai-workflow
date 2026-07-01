import { useEffect, useState } from "react";
import {
  deleteRepoApi,
  getSettings,
  importRepos,
  listSettingRepos,
  putSettings,
  setRepoEnabled,
  testConnection,
  upsertRepo,
} from "../api";
import type { Repo, SettingsView } from "../types";
import ClaudeToken from "./ClaudeToken";

// 设置各分区的锚点（电梯导航用）。
const SECTIONS: [string, string][] = [
  ["sec-source", "票源"],
  ["sec-jira", "JIRA"],
  ["sec-linear", "Linear"],
  ["sec-github", "GitHub"],
  ["sec-claude", "Claude 令牌"],
  ["sec-access", "访问控制"],
  ["sec-run", "运行"],
  ["sec-statusmap", "状态联动"],
  ["sec-repos", "登记仓库"],
];

// 设置中心：票源、各凭据（掩码）、按 URL 管理仓、连接测试。
// onReposChanged：登记仓变更后通知外层刷新候选票的选仓下拉。
export default function Settings({ onReposChanged }: { onReposChanged?: () => void }) {
  const [s, setS] = useState<SettingsView | null>(null);
  const [repos, setRepos] = useState<Repo[]>([]);
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);

  // 表单字段（密钥留空=不改）
  const [f, setF] = useState<Record<string, string>>({});
  const set = (k: string, v: string) => setF((p) => ({ ...p, [k]: v }));

  // status_map 编辑（任务态→JIRA 流转名）
  const STATE_ROWS: [string, string][] = [
    ["running_b", "调查中"], ["awaiting_review", "等人审"], ["running_c", "实装中"],
    ["awaiting_pr_review", "等 PR 审查"], ["running_d", "修订中"],
    ["done", "完成"], ["adjudication", "待裁决"], ["skipped", "已跳过"],
  ];
  const [smap, setSmap] = useState<Record<string, string>>({});
  const [importOwner, setImportOwner] = useState("");
  const [confirmImport, setConfirmImport] = useState(false); // 内联确认，替代原生 confirm

  const loadRepos = () => listSettingRepos().then((r) => setRepos(r.repos || [])).catch(() => {});
  const load = () => {
    getSettings().then((v) => {
      setS(v);
      setF({
        source: v.source,
        jira_domain: v.jira_domain,
        jira_user: v.jira_user,
        jira_project: v.jira_project,
        linear_team: v.linear_team,
        max_concurrent: String(v.max_concurrent || ""),
        task_timeout_min: String(v.task_timeout_min || ""),
      });
      setSmap(v.status_map || {});
    }).catch((e) => setMsg("读取设置失败：" + e.message));
    loadRepos();
  };
  useEffect(load, []);

  const flash = (m: string) => {
    setMsg(m);
    setTimeout(() => setMsg(""), 4000);
  };

  // 仓变更后：重载本页 + 通知外层刷新下拉。
  const afterReposChange = () => {
    loadRepos();
    onReposChanged?.();
  };

  const goto = (id: string) => document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });

  const save = async () => {
    setBusy(true);
    try {
      // 只发非空（密钥空=不改）；source/project/team/domain/user 直接发
      const payload: Record<string, string> = {};
      for (const k of ["source", "jira_domain", "jira_user", "jira_project", "linear_team"])
        if (f[k] !== undefined) payload[k] = f[k];
      for (const k of ["jira_api_key", "linear_api_key", "github_token", "admin_token"])
        if (f[k]) payload[k] = f[k];
      const p2 = payload as Record<string, unknown>;
      if (f.max_concurrent) p2.max_concurrent = Number(f.max_concurrent);
      if (f.task_timeout_min) p2.task_timeout_min = Number(f.task_timeout_min);
      p2.status_map = Object.fromEntries(Object.entries(smap).filter(([, val]) => val));
      const v = await putSettings(p2);
      setS(v);
      setSmap(v.status_map || {});
      setF((p) => ({ ...p, jira_api_key: "", linear_api_key: "", github_token: "", admin_token: "" }));
      flash("已保存并重载");
    } catch (e) {
      flash("保存失败：" + (e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const test = async (kind: "source" | "github") => {
    setBusy(true);
    try {
      const r = await testConnection(kind);
      flash(r.ok ? "✓ " + r.detail : "✗ " + r.error);
    } catch (e) {
      flash("✗ " + (e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  // 加/改仓表单（editing=true 时为编辑既有仓，键名只读、同名 PUT 覆盖）
  const [nr, setNr] = useState({ name: "", github: "", match: "", base: "" });
  const [editing, setEditing] = useState(false);
  const startEdit = (r: Repo) => {
    setEditing(true);
    setNr({ name: r.name, github: r.github, match: (r.match || []).join(", "), base: r.base || "" });
    goto("sec-repos");
  };
  const cancelEdit = () => {
    setEditing(false);
    setNr({ name: "", github: "", match: "", base: "" });
  };
  const addRepo = async () => {
    setBusy(true);
    try {
      await upsertRepo({
        name: nr.name.trim(),
        github: nr.github.trim(),
        match: nr.match.split(",").map((x) => x.trim()).filter(Boolean),
        base: nr.base.trim(),
      });
      setNr({ name: "", github: "", match: "", base: "" });
      setEditing(false);
      afterReposChange();
      flash(editing ? "仓已更新" : "仓已登记");
    } catch (e) {
      flash((editing ? "更新" : "登记") + "失败：" + (e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const delRepo = async (name: string) => {
    if (!confirm(`删除登记仓 ${name}？`)) return;
    await deleteRepoApi(name).catch(() => {});
    afterReposChange();
  };
  const toggleRepo = async (r: Repo) => {
    setBusy(true);
    try {
      await setRepoEnabled(r.name, !r.enabled);
      afterReposChange();
    } catch (e) {
      flash("切换失败：" + (e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const doImport = async () => {
    setConfirmImport(false);
    setBusy(true);
    try {
      const r = await importRepos(importOwner.trim() || undefined);
      afterReposChange();
      flash(`导入 ${r.owner}：新增 ${r.added}，已存在 ${r.skipped}（共 ${r.found}）`);
    } catch (e) {
      flash("导入失败：" + (e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  // 启用的排在前面，其次按名称——便于一眼看到生效中的仓。
  const sortedRepos = [...repos].sort(
    (a, b) => Number(!!b.enabled) - Number(!!a.enabled) || a.name.localeCompare(b.name),
  );

  if (!s) return <div className="muted pad">{msg || "加载设置…"}</div>;
  const mask = (set: boolean) => (set ? "已设置（留空不改）" : "未设置");

  return (
    <div className="settings">
      {/* 电梯导航：快速跳到各分区 */}
      <nav className="settings-nav">
        {SECTIONS.map(([id, label]) => (
          <button key={id} className="settings-nav-item" onClick={() => goto(id)}>{label}</button>
        ))}
      </nav>

      {msg && <div className="settings-flash">{msg}</div>}

      <section id="sec-source" className="card">
        <h3>票源</h3>
        <label>活跃源
          <select value={f.source || s.source} onChange={(e) => set("source", e.target.value)}>
            <option value="jira">JIRA</option>
            <option value="linear">Linear</option>
          </select>
        </label>
        <button className="mini" disabled={busy} onClick={() => test("source")}>测试票源连接</button>
      </section>

      <section id="sec-jira" className="card">
        <h3>JIRA</h3>
        <label>域名<input value={f.jira_domain ?? ""} onChange={(e) => set("jira_domain", e.target.value)} placeholder="xxx.atlassian.net" /></label>
        <label>账号<input value={f.jira_user ?? ""} onChange={(e) => set("jira_user", e.target.value)} placeholder="email" /></label>
        <label>项目<input value={f.jira_project ?? ""} onChange={(e) => set("jira_project", e.target.value)} placeholder="PROJ" /></label>
        <label>API Token<input type="password" value={f.jira_api_key ?? ""} onChange={(e) => set("jira_api_key", e.target.value)} placeholder={mask(s.jira_api_key_set)} /></label>
      </section>

      <section id="sec-linear" className="card">
        <h3>Linear</h3>
        <label>团队 key<input value={f.linear_team ?? ""} onChange={(e) => set("linear_team", e.target.value)} placeholder="ENG（可空）" /></label>
        <label>API Key<input type="password" value={f.linear_api_key ?? ""} onChange={(e) => set("linear_api_key", e.target.value)} placeholder={mask(s.linear_api_key_set)} /></label>
      </section>

      <section id="sec-github" className="card">
        <h3>GitHub</h3>
        <label>Token<input type="password" value={f.github_token ?? ""} onChange={(e) => set("github_token", e.target.value)} placeholder={mask(s.github_token_set)} /></label>
        <button className="mini" disabled={busy} onClick={() => test("github")}>测试 GitHub 连接</button>
      </section>

      <ClaudeToken />

      <section id="sec-access" className="card">
        <h3>访问控制</h3>
        <label>Admin Token<input type="password" value={f.admin_token ?? ""} onChange={(e) => set("admin_token", e.target.value)} placeholder={mask(s.admin_token_set)} /></label>
        <div className="hint">（已弃用）鉴权现由用户账号 + 会话接管；此项不再控制登录，仅保留兼容。</div>
      </section>

      <section id="sec-run" className="card">
        <h3>运行</h3>
        <label>最大并发任务数<input value={f.max_concurrent ?? ""} onChange={(e) => set("max_concurrent", e.target.value)} placeholder="3" /></label>
        <label>单任务超时(分钟)<input value={f.task_timeout_min ?? ""} onChange={(e) => set("task_timeout_min", e.target.value)} placeholder="60" /></label>
        <div className="hint">并发数改动需重启后端生效；超时对新任务即时生效。</div>
      </section>

      <section id="sec-statusmap" className="card">
        <h3>JIRA 状态联动（status_map）</h3>
        <div className="hint">任务进入某状态时，把工单流转到对应 JIRA 流转名；留空=不联动。</div>
        <table className="repo-table">
          <tbody>
            {STATE_ROWS.map(([k, label]) => (
              <tr key={k}>
                <td style={{ width: 120 }}>{label}<span className="muted2"> ({k})</span></td>
                <td><input className="smap-input" value={smap[k] || ""} placeholder="JIRA 流转名（如 开发中）"
                  onChange={(e) => setSmap({ ...smap, [k]: e.target.value })} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section id="sec-repos" className="card">
        <h3>登记仓库（按 GitHub 地址）</h3>
        <div className="repo-import">
          <input placeholder="owner/组织（留空按现有仓推断）" value={importOwner}
            onChange={(e) => setImportOwner(e.target.value)} disabled={confirmImport} />
          {!confirmImport ? (
            <button className="mini" disabled={busy} onClick={() => setConfirmImport(true)}>导入该组织全部仓库</button>
          ) : (
            <span className="repo-import-confirm">
              导入 <b>{importOwner.trim() || "默认组织"}</b> 全部仓库（默认停用）？
              <button className="primary mini" disabled={busy} onClick={doImport}>确认</button>
              <button className="mini" disabled={busy} onClick={() => setConfirmImport(false)}>取消</button>
            </span>
          )}
          <span className="hint" style={{ margin: 0 }}>自动登记的仓默认<b>不启用</b>，按需在下方开启。</span>
        </div>
        <div className="repo-add">
          <input placeholder="键名(如 channel)" value={nr.name} readOnly={editing} title={editing ? "编辑时键名不可改" : ""} onChange={(e) => setNr({ ...nr, name: e.target.value })} />
          <input placeholder="owner/repo" value={nr.github} onChange={(e) => setNr({ ...nr, github: e.target.value })} />
          <input placeholder="标题关键词,逗号分隔" value={nr.match} onChange={(e) => setNr({ ...nr, match: e.target.value })} />
          <input placeholder="PR base(可空,如 stage)" value={nr.base} onChange={(e) => setNr({ ...nr, base: e.target.value })} />
          <button className="primary mini" disabled={busy || !nr.name || !nr.github} onClick={addRepo}>{editing ? "更新" : "登记"}</button>
          {editing && <button className="mini" disabled={busy} onClick={cancelEdit}>取消</button>}
        </div>
        <div className="hint">无需本地预克隆——首次起任务时自动克隆并维护。PR base 留空则由目标仓 branch.md/默认分支决定。{editing && "（编辑中：键名不可改，保存即同名覆盖）"}</div>
        <table className="repo-table">
          <tbody>
            {sortedRepos.map((r) => (
              <tr key={r.name} className={r.enabled ? "" : "repo-off"}>
                <td className="col-toggle">
                  <button
                    className={"toggle " + (r.enabled ? "on" : "off")}
                    disabled={busy}
                    title={r.enabled ? "点击停用" : "点击启用"}
                    onClick={() => toggleRepo(r)}
                  >
                    {r.enabled ? "启用中" : "已停用"}
                  </button>
                </td>
                <td><b>{r.name}</b></td>
                <td className="col-gh">{r.github}</td>
                <td className="muted2">{(r.match || []).join(" / ")}</td>
                <td className="muted2">{r.base ? "base:" + r.base : ""}</td>
                <td className="col-ops">
                  <button className="mini" onClick={() => startEdit(r)}>编辑</button>
                  <button className="mini danger" onClick={() => delRepo(r.name)}>删</button>
                </td>
              </tr>
            ))}
            {sortedRepos.length === 0 && <tr><td colSpan={6} className="muted">暂无登记仓</td></tr>}
          </tbody>
        </table>
      </section>

      {/* 常驻保存：滚动时始终可见 */}
      <div className="settings-actions">
        <button className="primary" disabled={busy} onClick={save}>保存设置</button>
      </div>
    </div>
  );
}
