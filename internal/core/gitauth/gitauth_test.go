package gitauth

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestEmptyTokenNoAuth(t *testing.T) {
	if ConfigArgs("") != nil || ConfigEnv("") != nil {
		t.Fatal("空 token 应不产生任何认证参数/环境")
	}
}

func TestConfigArgsHeader(t *testing.T) {
	args := ConfigArgs("tok123")
	if len(args) != 2 || args[0] != "-c" {
		t.Fatalf("应为 [-c http.<url>.extraheader=...]，得 %v", args)
	}
	want := base64.StdEncoding.EncodeToString([]byte("x-access-token:tok123"))
	if !strings.Contains(args[1], "http.https://github.com/.extraheader=Authorization: Basic "+want) {
		t.Fatalf("extraheader 值不符：%q", args[1])
	}
}

func TestConfigEnvKeys(t *testing.T) {
	env := ConfigEnv("tok123")
	want := base64.StdEncoding.EncodeToString([]byte("x-access-token:tok123"))
	joined := strings.Join(env, "\n")
	for _, must := range []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + want,
	} {
		if !strings.Contains(joined, must) {
			t.Fatalf("缺 %q，得 %v", must, env)
		}
	}
	// EnvKeys 应覆盖 ConfigEnv 写入的所有键（供注入前剔除）
	if len(EnvKeys) != 3 {
		t.Fatalf("EnvKeys 应有 3 项，得 %v", EnvKeys)
	}
}
