import { useEffect, useState } from "react";
import {
  getMyClaudeToken,
  getTenantClaudeToken,
  isAdmin,
  putMyClaudeToken,
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

// ClaudeToken：公司共享令牌（仅管理员可设）+ 个人令牌（本人）。均为「登录态」令牌，非 API。
export default function ClaudeToken() {
  return (
    <section id="sec-claude" className="card">
      <h3>Claude 登录态令牌</h3>
      <div className="hint">
        用订阅登录态令牌（<code>claude setup-token</code> 生成）跑 agent，非 API 计费。
        解析优先级：<b>个人令牌 &gt; 公司共享令牌</b>。令牌加密存储，不回显明文。
      </div>
      {isAdmin() && (
        <TokenRow
          label="公司共享令牌（管理员）"
          hint="全租户成员默认使用；未填个人令牌者回落到它。共享一个订阅即共享其速率额度。"
          probe={getTenantClaudeToken}
          save={(t) => putTenantClaudeToken(t).then((r) => ({ configured: r.configured }))}
        />
      )}
      <TokenRow
        label="我的个人令牌"
        hint="填了则本人任务优先用它（独立额度）；留空则用公司共享令牌。"
        probe={getMyClaudeToken}
        save={(t) => putMyClaudeToken(t).then((r) => ({ configured: r.configured }))}
      />
    </section>
  );
}
