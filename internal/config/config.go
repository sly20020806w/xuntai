package config

import "os"

type Config struct {
	HTTPAddr string
	MySQLDSN string
}

func Load() Config {
	addr := os.Getenv("XUNTAI_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	return Config{
		HTTPAddr: addr,
		MySQLDSN: os.Getenv("XUNTAI_MYSQL_DSN"),
	}
}
