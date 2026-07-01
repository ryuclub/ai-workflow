import { useState } from "react";
import { clearToken, getSource, setToken } from "../api";

// 鉴权开启时的登录：输入 admin token，校验通过后进入。
export default function Login({ onOk }: { onOk: () => void }) {
  const [t, setT] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    setToken(t.trim());
    try {
      await getSource(); // 用受保护接口验证 token
      onOk();
    } catch {
      clearToken(); // 无效 token 不留存
      setErr("Token 无效");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login-wrap">
      <form className="login-card" onSubmit={submit}>
        <h2>AI 工作流流水线</h2>
        <p className="muted">该实例已启用访问控制，请输入 Admin Token</p>
        <input type="password" autoFocus value={t} onChange={(e) => setT(e.target.value)} placeholder="Admin Token" />
        {err && <div className="err">{err}</div>}
        <button className="primary" disabled={busy || !t.trim()}>进入</button>
      </form>
    </div>
  );
}
