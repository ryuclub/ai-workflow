import { useState } from "react";
import { login, setPlatformAdmin, setRole, setToken } from "../api";

// 邮箱+密码登录：校验通过后存会话 token 与角色，进入。
export default function Login({ onOk }: { onOk: () => void }) {
  const [email, setEmail] = useState("");
  const [pw, setPw] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const r = await login(email.trim(), pw);
      setToken(r.token);
      setRole(r.role);
      setPlatformAdmin(r.user.platform_admin);
      onOk();
    } catch (e) {
      setErr(e instanceof Error ? e.message : "登录失败");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login-wrap">
      <form className="login-card" onSubmit={submit}>
        <h2>PR 工厂</h2>
        <p className="muted">请使用邮箱与密码登录</p>
        <input
          type="email"
          autoFocus
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="邮箱"
        />
        <input
          type="password"
          value={pw}
          onChange={(e) => setPw(e.target.value)}
          placeholder="密码"
        />
        {err && <div className="err">{err}</div>}
        <button className="primary" disabled={busy || !email.trim() || !pw}>
          {busy ? "登录中…" : "登录"}
        </button>
      </form>
    </div>
  );
}
