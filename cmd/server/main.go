package main

import (
	"log"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"xuntai/internal/config"
	"xuntai/internal/httpserver"
	"xuntai/internal/model"
)

func main() {
	cfg := config.Load()
	if cfg.MySQLDSN != "" {
		db, err := gorm.Open(mysql.Open(cfg.MySQLDSN), &gorm.Config{})
		if err != nil {
			log.Fatal(err)
		}
		if err := model.Migrate(db); err != nil {
			log.Fatal(err)
		}
	}
	if err := httpserver.Run(cfg.HTTPAddr); err != nil {
		log.Fatal(err)
	}
}
