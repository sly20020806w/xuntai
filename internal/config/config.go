package config

import "os"

type Config struct {
	HTTPAddr      string
	MySQLDSN      string
	SQLitePath    string
	JWTSecret     string
	AdminPassword string
}

func Load() Config {
	addr := os.Getenv("XUNTAI_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	sqlitePath := os.Getenv("XUNTAI_SQLITE_PATH")
	if sqlitePath == "" {
		sqlitePath = "data/xuntai.db"
	}
	secret := os.Getenv("XUNTAI_JWT_SECRET")
	if secret == "" {
		secret = "xuntai-dev-secret"
	}
	return Config{
		HTTPAddr:      addr,
		MySQLDSN:      os.Getenv("XUNTAI_MYSQL_DSN"),
		SQLitePath:    sqlitePath,
		JWTSecret:     secret,
		AdminPassword: os.Getenv("XUNTAI_ADMIN_PASSWORD"),
	}
}
