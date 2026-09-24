package base

import (
	"xuntai/internal/model"

	"gorm.io/gorm"
)

// Grant 把还没挂上的接口补到角色上。已有的绑定会跳过。
func Grant(db *gorm.DB, roleName string, specs []model.API) error {
	var role model.Role
	if err := db.Where("name = ?", roleName).First(&role).Error; err != nil {
		return err
	}
	var existing []model.API
	if err := db.Model(&role).Association("APIs").Find(&existing); err != nil {
		return err
	}
	have := make(map[string]bool, len(existing))
	for _, api := range existing {
		have[api.Method+" "+api.Path] = true
	}
	var add []model.API
	for _, spec := range specs {
		if have[spec.Method+" "+spec.Path] {
			continue
		}
		var row model.API
		if err := db.Where("method = ? AND path = ?", spec.Method, spec.Path).Limit(1).Find(&row).Error; err != nil {
			return err
		}
		if row.ID == 0 {
			row = model.API{Method: spec.Method, Path: spec.Path}
			if err := db.Create(&row).Error; err != nil {
				return err
			}
		}
		add = append(add, row)
	}
	if len(add) == 0 {
		return nil
	}
	return db.Model(&role).Association("APIs").Append(add)
}
