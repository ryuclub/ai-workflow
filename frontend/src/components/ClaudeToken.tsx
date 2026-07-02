import { useEffect, useState } from "react";
import {
  getMyClaudeToken,
  getMyGithubToken,
  getTenantClaudeToken,
  isAdmin,
  putMyClaudeToken,
  putMyGithubToken,
  putTenantClaudeToken,
} from "../api";

// TokenRow 管理单个登录态令牌（探测是否已配置、设置新值、清除）。不回显明文。
function TokenRow({
  label,
  hint,
  probe,
  save,
}: {
  label: string;
  hint: string;
  probe: () => Promise<{ configured: boolean }>;
  save: (token: string) => Promise<{ configured: boolean }>;
}) {
  const [configured, setConfigured] = useState<boolean | null>(null);
  const [val, setVal] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [disabled, setDisabled] = useState(false); // 未配 MASTER_KEY 等

  const load = () =>
    probe()
      .then((r) => setConfigured(r.configured))
      .catch((e) => {
        setDisabled(true);
        setMsg(e instanceof Error ? e.message : "不可用");
      });
  useEffect(() => {
    load();
  }, []);

  const doSave = async (token: string) => {
    setBusy(true);
    setMsg("");
    try {
      const r = await save(token);
      setConfigured(r.configured);
      setVal("");
      setMsg(token ? "已保存" : "已清除");
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "保存失败");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="token-row">
      <label>
        {label}
        <input
          type="password"
          value={val}
          disabled={disabled || busy}
          onChange={(e) => setVal(e.target.value)}
          placeholder={configured ? "已配置（留空不改）" : "未配置，粘贴令牌"}
        />
      </label>
      <div className="row-actions">
        <button className="mini primary" disabled={disabled || busy || !val.trim()} onClick={() => doSave(val.trim())}>
          保存
        </button>
        {configured && (
          <button className="mini" disabled={disabled || busy} onClick={() => doSave("")}>
            清除
          </button>
        )}
        {msg && <span className="muted">{msg}</span>}
      </div>
      <div className="hint">{hint}</div>
    </div>
  );
}

// ClaudeToken：登录态令牌与个人凭据。Claude 令牌(公司共享+个人)；GitHub token 个人覆盖。
// 均「公司共享 > 个人」两级(GitHub 公司令牌在上面「GitHub」分区设),加密存储不回显。
export default function ClaudeToken() {
  return (
    <section id="sec-claude" className="card">
      <h3>登录态令牌 / 个人凭据</h3>
      <div className="hint">
        Claude 用订阅登录态令牌（<code>claude setup-token</code> 生成）跑 agent，非 API 计费。
        解析优先级均为 <b>个人 &gt; 公司共享</b>。令牌加密存储，不回显明文。
      </div>
      {isAdmin() && (
        <TokenRow
          label="Claude 公司共享令牌（管理员）"
          hint="全公司成员默认使用；未填个人令牌者回落到它。共享一个订阅即共享其速率额度。"
          probe={getTenantClaudeToken}
          save={(t) => putTenantClaudeToken(t).then((r) => ({ configured: r.configured }))}
        />
      )}
      <TokenRow
        label="我的 Claude 个人令牌"
        hint="填了则本人任务优先用它（独立额度）；留空则用公司共享令牌。"
        probe={getMyClaudeToken}
        save={(t) => putMyClaudeToken(t).then((r) => ({ configured: r.configured }))}
      />
      <TokenRow
        label="我的 GitHub 个人 token"
        hint="填了则本人任务的 GitHub 操作（建 PR / 打标签等）用自己的身份；留空则用公司共享 GitHub token。"
        probe={getMyGithubToken}
        save={(t) => putMyGithubToken(t).then((r) => ({ configured: r.configured }))}
      />
    </section>
  );
}
