package store

import (
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"xuntai/internal/config"
)

// Open 有 MySQL 连接串时走 MySQL，否则用本地 SQLite。
func Open(cfg config.Config) (*gorm.DB, error) {
	if cfg.MySQLDSN != "" {
		return gorm.Open(mysql.Open(cfg.MySQLDSN), &gorm.Config{})
	}
	if err := os.MkdirAll(filepath.Dir(cfg.SQLitePath), 0o755); err != nil {
		return nil, err
	}
	return gorm.Open(sqlite.Open(cfg.SQLitePath), &gorm.Config{})
}
