package secret

import "testing"

// fakeStore 是内存版 Store，用于测试加密 round-trip 与两级解析。
type fakeStore struct {
	tenant map[string][]byte
	user   map[string][]byte
}

func newFake() *fakeStore {
	return &fakeStore{tenant: map[string][]byte{}, user: map[string][]byte{}}
}
func (f *fakeStore) PutTenantSecret(t, k string, enc []byte) error { f.tenant[t+"/"+k] = enc; return nil }
func (f *fakeStore) GetTenantSecret(t, k string) ([]byte, error)   { return f.tenant[t+"/"+k], nil }
func (f *fakeStore) PutUserSecret(u, k string, enc []byte) error   { f.user[u+"/"+k] = enc; return nil }
func (f *fakeStore) GetUserSecret(u, k string) ([]byte, error)     { return f.user[u+"/"+k], nil }

func TestNilWhenNoMasterKey(t *testing.T) {
	v, err := New("", newFake())
	if err != nil || v != nil {
		t.Fatalf("空 MASTER_KEY 应返回 (nil,nil)，得 (%v,%v)", v, err)
	}
}

func TestRoundTripAndCiphertext(t *testing.T) {
	f := newFake()
	v, err := New("master-passphrase", f)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.SetTenant("t1", KeyClaudeToken, "sk-token-abc"); err != nil {
		t.Fatal(err)
	}
	// 落库应为密文，不含明文
	if string(f.tenant["t1/"+KeyClaudeToken]) == "sk-token-abc" {
		t.Fatal("凭据未加密即落库")
	}
	if !v.HasTenant("t1", KeyClaudeToken) {
		t.Fatal("应探测到已配置")
	}
	if got := v.getTenant("t1", KeyClaudeToken); got != "sk-token-abc" {
		t.Fatalf("解密不符：%q", got)
	}
}

func TestResolveTwoTier(t *testing.T) {
	v, _ := New("k", newFake())
	// 都未配 → 空
	if v.ResolveClaudeToken("t1", "u1") != "" {
		t.Fatal("未配应为空")
	}
	// 仅租户共享 → 用共享
	_ = v.SetTenant("t1", KeyClaudeToken, "tenant-tok")
	if got := v.ResolveClaudeToken("t1", "u1"); got != "tenant-tok" {
		t.Fatalf("应回落租户共享，得 %q", got)
	}
	// 用户个人 → 覆盖租户
	_ = v.SetUser("u1", KeyClaudeToken, "user-tok")
	if got := v.ResolveClaudeToken("t1", "u1"); got != "user-tok" {
		t.Fatalf("个人应覆盖租户，得 %q", got)
	}
	// 另一个没个人令牌的用户 → 仍用租户共享
	if got := v.ResolveClaudeToken("t1", "u2"); got != "tenant-tok" {
		t.Fatalf("u2 应回落租户，得 %q", got)
	}
}

// TestAADRejectsCrossSlot：把租户 A 的密文搬到租户 B 的槽，解密应失败（AAD 绑定）。
func TestAADRejectsCrossSlot(t *testing.T) {
	f := newFake()
	v, _ := New("k", f)
	_ = v.SetTenant("tA", KeyClaudeToken, "tokA")
	// 具写库权者把 tA 的密文原样拷进 tB 的槽
	f.tenant["tB/"+KeyClaudeToken] = f.tenant["tA/"+KeyClaudeToken]
	if got := v.getTenant("tB", KeyClaudeToken); got != "" {
		t.Fatalf("跨槽搬运的密文不应解出，得 %q", got)
	}
	// tA 自身仍可正常解
	if got := v.getTenant("tA", KeyClaudeToken); got != "tokA" {
		t.Fatalf("原槽应正常解密，得 %q", got)
	}
}

// TestResolveGithubTwoTier：GitHub token 个人 > 公司共享 > 空。
func TestResolveGithubTwoTier(t *testing.T) {
	v, _ := New("k", newFake())
	// 无个人 → 用传入的公司共享
	if got := v.ResolveGithubToken("u1", "shared-tok"); got != "shared-tok" {
		t.Fatalf("应回落公司共享，得 %q", got)
	}
	// 有个人 → 覆盖
	_ = v.SetUser("u1", KeyGithubToken, "my-tok")
	if got := v.ResolveGithubToken("u1", "shared-tok"); got != "my-tok" {
		t.Fatalf("个人应覆盖公司，得 %q", got)
	}
	// 另一用户无个人 → 仍用公司共享
	if got := v.ResolveGithubToken("u2", "shared-tok"); got != "shared-tok" {
		t.Fatalf("u2 应回落公司，得 %q", got)
	}
	// userID 空(如后台轮询) → 直接公司共享
	if got := v.ResolveGithubToken("", "shared-tok"); got != "shared-tok" {
		t.Fatalf("空用户应用公司共享，得 %q", got)
	}
}

func TestWrongKeyCannotDecrypt(t *testing.T) {
	f := newFake()
	v1, _ := New("key-A", f)
	_ = v1.SetTenant("t1", KeyClaudeToken, "secret")
	v2, _ := New("key-B", f) // 换主密钥
	if v2.getTenant("t1", KeyClaudeToken) != "" {
		t.Fatal("错误主密钥不应能解密")
	}
}
