package config

import (
	"testing"
)

func TestRolloutsMockStaysOff(t *testing.T) {
	t.Setenv("XUNTAI_ROLLOUTS_MOCK", "")
	if RolloutsMock() {
		t.Fatal("未设置时不应该模拟灰度")
	}
	t.Setenv("XUNTAI_ROLLOUTS_MOCK", "off")
	if RolloutsMock() {
		t.Fatal("关闭时不应该模拟灰度")
	}
	t.Setenv("XUNTAI_ROLLOUTS_MOCK", "1")
	if !RolloutsMock() {
		t.Fatal("打开后应该模拟灰度")
	}
}

func TestLoadRequiresJWTSecret(t *testing.T) {
	t.Setenv("XUNTAI_JWT_SECRET", "")
	if _, err := Load(); err == nil {
		t.Fatal("未设置登录密钥时不应该启动")
	}
}

func TestLoadKeepsMySQLAndScope(t *testing.T) {
	t.Setenv("XUNTAI_JWT_SECRET", "unit-test-secret")
	t.Setenv("XUNTAI_MYSQL_DSN", "user:pass@tcp(127.0.0.1:3306)/xuntai")
	t.Setenv("XUNTAI_SCOPE_FILTER", "0")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JWTSecret != "unit-test-secret" || cfg.MySQLDSN == "" || cfg.ScopeFilter {
		t.Fatalf("配置 = %+v", cfg)
	}
}
