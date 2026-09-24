package db

import (
	"xuntai/internal/base"
	"xuntai/internal/model"

	"gorm.io/gorm"
)

func APIs() []model.API {
	return []model.API{
		{Method: "GET", Path: "/api/db/instances"},
		{Method: "POST", Path: "/api/db/instances"},
		{Method: "GET", Path: "/api/db/backups"},
		{Method: "POST", Path: "/api/db/backups"},
		{Method: "GET", Path: "/api/db/restores"},
		{Method: "POST", Path: "/api/db/restores"},
	}
}

// Seed 补上数据库接口。还没有实例时，在订单叶子登记一主一从和一次全量备份，支付叶子只登记主库。
// 不保存口令，不写复制延迟，也不生成还原记录。
func Seed(db *gorm.DB) (bool, error) {
	for _, role := range []string{"平台管理员", "节点运维"} {
		if err := base.Grant(db, role, APIs()); err != nil {
			return false, err
		}
	}
	var count int64
	if err := db.Model(&model.Instance{}).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		orderNode, err := nodeID(tx, "订单")
		if err != nil {
			return err
		}
		payNode, err := nodeID(tx, "支付")
		if err != nil {
			return err
		}
		master := model.Instance{
			Name: "订单主库", TreeNodeID: orderNode, Host: "10.8.2.17", Port: 3306, Version: "8.0", Role: "主",
		}
		slave := model.Instance{
			Name: "订单从库", TreeNodeID: orderNode, Host: "10.8.2.18", Port: 3306, Version: "8.0", Role: "从",
		}
		pay := model.Instance{
			Name: "支付主库", TreeNodeID: payNode, Host: "10.8.3.9", Port: 3306, Version: "8.0", Role: "主",
		}
		if err := tx.Create(&master).Error; err != nil {
			return err
		}
		slave.MasterID = master.ID
		if err := tx.Create(&slave).Error; err != nil {
			return err
		}
		if err := tx.Create(&pay).Error; err != nil {
			return err
		}
		backup := model.Backup{InstanceID: master.ID, Kind: "全量", Keep: 7, Status: "已登记"}
		return tx.Create(&backup).Error
	})
	return err == nil, err
}

func nodeID(tx *gorm.DB, name string) (uint, error) {
	var node model.Node
	if err := tx.Where("name = ?", name).First(&node).Error; err != nil {
		return 0, err
	}
	return node.ID, nil
}
