import { useEffect, useState } from "react";
import { addMember, listMembers, type Member, removeMember, setMemberRole } from "../api";

// Members：租户管理员管理本公司成员——列表、加成员、改角色、移除。
export default function Members() {
  const [members, setMembers] = useState<Member[]>([]);
  const [email, setEmail] = useState("");
  const [pw, setPw] = useState("");
  const [role, setRole] = useState("member");
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);

  const load = () =>
    listMembers()
      .then((r) => setMembers(r.members || []))
      .catch((e) => setMsg(e instanceof Error ? e.message : "加载失败"));
  useEffect(() => {
    load();
  }, []);

  const add = async () => {
    setBusy(true);
    setMsg("");
    try {
      await addMember(email.trim().toLowerCase(), pw, role);
      setEmail("");
      setPw("");
      setRole("member");
      await load();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "添加失败");
    } finally {
      setBusy(false);
    }
  };

  const changeRole = async (uid: string, r: string) => {
    try {
      await setMemberRole(uid, r);
      await load();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "改角色失败");
    }
  };

  const remove = async (uid: string) => {
    try {
      await removeMember(uid);
      await load();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "移除失败");
    }
  };

  return (
    <section id="sec-members" className="card">
      <h3>成员管理</h3>
      <div className="hint">管理本公司成员：新增员工（邮箱+初始密码）、设角色（管理员/成员）、移除。</div>
      <table className="members">
        <thead>
          <tr>
            <th>邮箱</th>
            <th>角色</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {members.map((m) => (
            <tr key={m.user_id}>
              <td>{m.email}</td>
              <td>
                <select value={m.role} onChange={(e) => changeRole(m.user_id, e.target.value)}>
                  <option value="member">成员</option>
                  <option value="admin">管理员</option>
                </select>
              </td>
              <td>
                <button className="mini" onClick={() => remove(m.user_id)}>
                  移除
                </button>
              </td>
            </tr>
          ))}
          {members.length === 0 && (
            <tr>
              <td colSpan={3} className="muted">
                暂无成员
              </td>
            </tr>
          )}
        </tbody>
      </table>
      <div className="add-member">
        <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="新成员邮箱" />
        <input type="password" value={pw} onChange={(e) => setPw(e.target.value)} placeholder="初始密码（已存在用户可留空）" />
        <select value={role} onChange={(e) => setRole(e.target.value)}>
          <option value="member">成员</option>
          <option value="admin">管理员</option>
        </select>
        <button className="mini primary" disabled={busy || !email.trim()} onClick={add}>
          添加
        </button>
      </div>
      {msg && <div className="err">{msg}</div>}
    </section>
  );
}
