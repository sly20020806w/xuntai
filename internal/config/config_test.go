package config

import (
	"testing"
)

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
