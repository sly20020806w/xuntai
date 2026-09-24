package config

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	HTTPAddr      string
	MySQLDSN      string
	SQLitePath    string
	JWTSecret     string
	AdminPassword string
	ScopeFilter   bool
}

func Load() (Config, error) {
	loadEnvFile()
	secret := strings.TrimSpace(os.Getenv("XUNTAI_JWT_SECRET"))
	if secret == "" {
		return Config{}, errors.New("必须设置 XUNTAI_JWT_SECRET")
	}
	addr := os.Getenv("XUNTAI_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	sqlitePath := os.Getenv("XUNTAI_SQLITE_PATH")
	if sqlitePath == "" {
		sqlitePath = "data/xuntai.db"
	}
	return Config{
		HTTPAddr:      addr,
		MySQLDSN:      os.Getenv("XUNTAI_MYSQL_DSN"),
		SQLitePath:    sqlitePath,
		JWTSecret:     secret,
		AdminPassword: os.Getenv("XUNTAI_ADMIN_PASSWORD"),
		ScopeFilter:   scopeFilter(),
	}, nil
}

func scopeFilter() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("XUNTAI_SCOPE_FILTER"))) {
	case "0", "false", "off":
		return false
	default:
		return true
	}
}

func loadEnvFile() {
	raw, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, value)
	}
}
