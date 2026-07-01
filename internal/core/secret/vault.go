// Package secret 提供租户/用户凭据的加密存储与「登录态令牌」两级解析。
// 加密：AES-256-GCM，密钥由 MASTER_KEY 经 SHA-256 派生（任意口令→32 字节）。
// 明文不落库、不出本包；store 层只见密文块。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
)

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
	sum := sha256.Sum256([]byte(masterKey)) // 任意口令 → 32 字节 AES-256 密钥
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{gcm: gcm, st: st}, nil
}

func (v *Vault) seal(plaintext string) ([]byte, error) {
	nonce := make([]byte, v.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return v.gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil // nonce||ciphertext
}

func (v *Vault) open(blob []byte) (string, error) {
	ns := v.gcm.NonceSize()
	if len(blob) < ns {
		return "", errors.New("密文长度不足")
	}
	pt, err := v.gcm.Open(nil, blob[:ns], blob[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// SetTenant / SetUser 加密并写入。空明文视为删除（此处简化为写入空块由上层控制）。
func (v *Vault) SetTenant(tenantID, key, plaintext string) error {
	enc, err := v.seal(plaintext)
	if err != nil {
		return err
	}
	return v.st.PutTenantSecret(tenantID, key, enc)
}

func (v *Vault) SetUser(userID, key, plaintext string) error {
	enc, err := v.seal(plaintext)
	if err != nil {
		return err
	}
	return v.st.PutUserSecret(userID, key, enc)
}

// getTenant / getUser 返回明文；未配置返回 ""。
func (v *Vault) getTenant(tenantID, key string) string {
	enc, err := v.st.GetTenantSecret(tenantID, key)
	if err != nil || enc == nil {
		return ""
	}
	pt, err := v.open(enc)
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
	pt, err := v.open(enc)
	if err != nil {
		return ""
	}
	return pt
}

// HasTenant / HasUser 用于「是否已配置」探测（不回明文）。
func (v *Vault) HasTenant(tenantID, key string) bool { return v.getTenant(tenantID, key) != "" }
func (v *Vault) HasUser(userID, key string) bool     { return v.getUser(userID, key) != "" }

// ResolveClaudeToken 两级解析登录态令牌：员工个人令牌优先，回落租户共享令牌；都无返回 ""。
func (v *Vault) ResolveClaudeToken(tenantID, userID string) string {
	if userID != "" {
		if tok := v.getUser(userID, KeyClaudeToken); tok != "" {
			return tok
		}
	}
	return v.getTenant(tenantID, KeyClaudeToken)
}
