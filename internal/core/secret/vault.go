// Package secret 提供租户/用户凭据的加密存储与「登录态令牌」两级解析。
// 加密：AES-256-GCM；密钥由 MASTER_KEY 经 scrypt(慢哈希+盐) 派生，
// 抬高拖库后离线爆破成本。MASTER_KEY 仍应为高熵随机串（如 openssl rand -hex 32），勿用弱口令。
// 每条密文以其归属（tenant/user + key）为 GCM AAD 绑定，防具写库权者跨槽搬运密文。
// 明文不落库、不出本包；store 层只见密文块。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"

	"golang.org/x/crypto/scrypt"
)

// kdfSalt 是密钥派生的固定盐（防通用彩虹表；主防线仍是高熵 MASTER_KEY + scrypt 慢哈希）。
var kdfSalt = []byte("ai-workflow/secret/v1")

// KeyClaudeToken 是 Claude 登录态 OAuth 令牌（claude setup-token 生成）在凭据表里的键名。
const KeyClaudeToken = "CLAUDE_OAUTH_TOKEN"

// Store 是密文凭据的持久化接口（由 store.SQLite 实现）。Get 未命中返回 (nil, nil)。
type Store interface {
	PutTenantSecret(tenantID, key string, enc []byte) error
	GetTenantSecret(tenantID, key string) ([]byte, error)
	PutUserSecret(userID, key string, enc []byte) error
	GetUserSecret(userID, key string) ([]byte, error)
}

// Vault 用主密钥加解密并读写凭据。
type Vault struct {
	gcm cipher.AEAD
	st  Store
}

// New 据 MASTER_KEY 口令与 store 构造 Vault。masterKey 为空表示未配主密钥（禁用凭据存储）。
func New(masterKey string, st Store) (*Vault, error) {
	if masterKey == "" {
		return nil, nil // 上层据 nil 判断「未启用凭据存储」
	}
	// scrypt 慢哈希派生 32 字节 AES-256 密钥（仅启动时一次，无运行期开销）。
	key, err := scrypt.Key([]byte(masterKey), kdfSalt, 1<<15, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{gcm: gcm, st: st}, nil
}

// aad 把密文绑定到其归属槽（scope/id/key），作为 GCM 附加认证数据，
// 使密文换到别的槽后解密即失败，堵住具写库权者的跨槽搬运。
func aad(scope, id, key string) []byte { return []byte(scope + "/" + id + "/" + key) }

func (v *Vault) seal(plaintext string, aad []byte) ([]byte, error) {
	nonce := make([]byte, v.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return v.gcm.Seal(nonce, nonce, []byte(plaintext), aad), nil // nonce||ciphertext
}

func (v *Vault) open(blob, aad []byte) (string, error) {
	ns := v.gcm.NonceSize()
	if len(blob) < ns {
		return "", errors.New("密文长度不足")
	}
	pt, err := v.gcm.Open(nil, blob[:ns], blob[ns:], aad)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// SetTenant / SetUser 加密并写入。空明文写入空块，功能上等价「已清除」。
func (v *Vault) SetTenant(tenantID, key, plaintext string) error {
	enc, err := v.seal(plaintext, aad("tenant", tenantID, key))
	if err != nil {
		return err
	}
	return v.st.PutTenantSecret(tenantID, key, enc)
}

func (v *Vault) SetUser(userID, key, plaintext string) error {
	enc, err := v.seal(plaintext, aad("user", userID, key))
	if err != nil {
		return err
	}
	return v.st.PutUserSecret(userID, key, enc)
}

// getTenant / getUser 返回明文；未配置或校验失败返回 ""。
func (v *Vault) getTenant(tenantID, key string) string {
	enc, err := v.st.GetTenantSecret(tenantID, key)
	if err != nil || enc == nil {
		return ""
	}
	pt, err := v.open(enc, aad("tenant", tenantID, key))
	if err != nil {
		return ""
	}
	return pt
}

func (v *Vault) getUser(userID, key string) string {
	enc, err := v.st.GetUserSecret(userID, key)
	if err != nil || enc == nil {
		return ""
	}
	pt, err := v.open(enc, aad("user", userID, key))
	if err != nil {
		return ""
	}
	return pt
}

// HasTenant / HasUser 用于「是否已配置」探测（不回明文）。
func (v *Vault) HasTenant(tenantID, key string) bool { return v.getTenant(tenantID, key) != "" }
func (v *Vault) HasUser(userID, key string) bool     { return v.getUser(userID, key) != "" }

// GetTenant 返回租户某凭据明文（未配置或校验失败返回 ""）。供 Runtime 构造租户级配置。
func (v *Vault) GetTenant(tenantID, key string) string { return v.getTenant(tenantID, key) }

// ResolveClaudeToken 两级解析登录态令牌：员工个人令牌优先，回落租户共享令牌；都无返回 ""。
func (v *Vault) ResolveClaudeToken(tenantID, userID string) string {
	if userID != "" {
		if tok := v.getUser(userID, KeyClaudeToken); tok != "" {
			return tok
		}
	}
	return v.getTenant(tenantID, KeyClaudeToken)
}
