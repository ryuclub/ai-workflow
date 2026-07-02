import { useEffect, useState } from "react";
import { createTenant, listTenants, type Tenant } from "../api";

// Platform：平台超管开通/查看租户(公司)。开通后把管理员账号交给该公司,
// 他们自己用「成员管理」加员工。
export default function Platform() {
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [pw, setPw] = useState("");
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);

  const load = () =>
    listTenants()
      .then((r) => setTenants(r.tenants || []))
      .catch((e) => setMsg(e instanceof Error ? e.message : "加载失败"));
  useEffect(() => {
    load();
  }, []);

  const create = async () => {
    setBusy(true);
    setMsg("");
    try {
      await createTenant(name.trim(), email.trim().toLowerCase(), pw);
      setMsg(`已开通「${name.trim()}」，管理员 ${email.trim().toLowerCase()}`);
      setName("");
      setEmail("");
      setPw("");
      await load();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "开通失败");
    } finally {
      setBusy(false);
    }
  };

  return (
    <section id="sec-platform" className="card">
      <h3>平台管理 · 租户开通</h3>
      <div className="hint">
        开通新公司(租户)并指定其首个管理员。开通后把管理员账号交给该公司，其管理员在「成员管理」自行加员工。
      </div>
      <table className="members">
        <thead>
          <tr>
            <th>公司</th>
            <th>租户 ID</th>
          </tr>
        </thead>
        <tbody>
          {tenants.map((t) => (
            <tr key={t.id}>
              <td>{t.name}</td>
              <td className="muted" style={{ fontFamily: "monospace", fontSize: 11 }}>{t.id}</td>
            </tr>
          ))}
          {tenants.length === 0 && (
            <tr>
              <td colSpan={2} className="muted">暂无租户</td>
            </tr>
          )}
        </tbody>
      </table>
      <div className="add-member">
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="公司名" />
        <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="管理员邮箱" />
        <input type="password" value={pw} onChange={(e) => setPw(e.target.value)} placeholder="初始密码(已存在用户可留空)" />
        <button className="mini primary" disabled={busy || !name.trim() || !email.trim()} onClick={create}>
          开通
        </button>
      </div>
      {msg && <div className="hint">{msg}</div>}
    </section>
  );
}
