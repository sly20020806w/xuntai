package base

import (
	"xuntai/internal/model"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const DefaultPassword = "xuntai-dev"

func Seed(db *gorm.DB, password string) (bool, error) {
	var count int64
	if err := db.Model(&model.User{}).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	if password == "" {
		password = DefaultPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}

	menus := []model.Menu{
		{Name: "底座", Path: "base"},
		{Name: "服务树", Path: "tree"},
		{Name: "工单", Path: "ticket"},
		{Name: "任务", Path: "task"},
		{Name: "监控", Path: "monitor"},
		{Name: "集群", Path: "k8s"},
		{Name: "发布", Path: "cicd"},
		{Name: "数据库", Path: "db"},
	}
	apis := []model.API{
		{Method: "GET", Path: "/api/base/me"},
		{Method: "GET", Path: "/api/base/menus"},
		{Method: "GET", Path: "/api/base/users"},
		{Method: "POST", Path: "/api/base/users"},
		{Method: "GET", Path: "/api/base/roles"},
		{Method: "POST", Path: "/api/base/roles"},
		{Method: "PUT", Path: "/api/base/roles/:id/menus"},
		{Method: "PUT", Path: "/api/base/roles/:id/apis"},
		{Method: "GET", Path: "/api/base/apis"},
		{Method: "POST", Path: "/api/base/menus"},
	}
	return true, db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&menus).Error; err != nil {
			return err
		}
		if err := tx.Create(&apis).Error; err != nil {
			return err
		}
		role := model.Role{Name: "平台管理员"}
		if err := tx.Create(&role).Error; err != nil {
			return err
		}
		if err := tx.Model(&role).Association("Menus").Replace(menus); err != nil {
			return err
		}
		if err := tx.Model(&role).Association("APIs").Replace(apis); err != nil {
			return err
		}
		user := model.User{Name: "周宁", PasswordHash: string(hash)}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		return tx.Model(&user).Association("Roles").Replace([]model.Role{role})
	})
}
