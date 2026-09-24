package store

import (
	"path/filepath"
	"strings"
	"testing"

	"xuntai/internal/config"
	"xuntai/internal/model"
)

func TestSQLitePathMigrates(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(config.Config{SQLitePath: filepath.Join(dir, "xuntai.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.Close()
}

func TestMySQLDriverIsUsedWhenDSNSet(t *testing.T) {
	_, err := Open(config.Config{MySQLDSN: "root:secret@tcp(127.0.0.1:1)/xuntai?charset=utf8mb4&parseTime=True&timeout=1s"})
	if err == nil {
		t.Fatal("没有 MySQL 服务时不应该打开成功")
	}
	if strings.Contains(err.Error(), "unknown driver") {
		t.Fatal(err)
	}
}
