package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"xuntai/internal/access"
	apibase "xuntai/internal/api/base"
	"xuntai/internal/base"
	"xuntai/internal/config"
	"xuntai/internal/httpserver"
	"xuntai/internal/model"
)

func main() {
	cfg := config.Load()
	db, err := openDB(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err := model.Migrate(db); err != nil {
		log.Fatal(err)
	}
	created, err := base.Seed(db, cfg.AdminPassword)
	if err != nil {
		log.Fatal(err)
	}
	if created {
		password := cfg.AdminPassword
		if password == "" {
			password = base.DefaultPassword
		}
		log.Printf("已创建初始用户 周宁，密码 %s", password)
	}
	gate := access.New()
	if err := gate.Reload(db); err != nil {
		log.Fatal(err)
	}
	if err := httpserver.Run(cfg.HTTPAddr, httpserver.Deps{
		Base: apibase.Deps{DB: db, Secret: cfg.JWTSecret, Gate: gate},
	}); err != nil {
		log.Fatal(err)
	}
}

func openDB(cfg config.Config) (*gorm.DB, error) {
	if cfg.MySQLDSN != "" {
		return gorm.Open(mysql.Open(cfg.MySQLDSN), &gorm.Config{})
	}
	if err := os.MkdirAll(filepath.Dir(cfg.SQLitePath), 0o755); err != nil {
		return nil, err
	}
	log.Printf("未配置 MySQL，使用本地库 %s", cfg.SQLitePath)
	return gorm.Open(sqlite.Open(cfg.SQLitePath), &gorm.Config{})
}
