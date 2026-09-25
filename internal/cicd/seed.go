package cicd

import (
	"xuntai/internal/base"
	"xuntai/internal/model"

	"gorm.io/gorm"
)

func APICatalog() []model.API {
	return []model.API{
		{Method: "GET", Path: "/api/cicd/items"},
		{Method: "POST", Path: "/api/cicd/items"},
		{Method: "GET", Path: "/api/cicd/items/:id"},
		{Method: "PUT", Path: "/api/cicd/items/:id"},
		{Method: "GET", Path: "/api/cicd/orders"},
		{Method: "POST", Path: "/api/cicd/orders"},
		{Method: "POST", Path: "/api/cicd/orders/:id/confirm"},
	}
}

// Seed 补上发布接口。还没有发布项时放入订单和支付，生产单停在生产阶段前。
func Seed(db *gorm.DB) (bool, error) {
	for _, role := range []string{"平台管理员", "节点运维"} {
		if err := base.Grant(db, role, APICatalog()); err != nil {
			return false, err
		}
	}
	var count int64
	if err := db.Model(&model.DeployItem{}).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return false, EnsureAll(db)
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
		orderItem := model.DeployItem{Name: "order-api", TreeNodeID: orderNode, Repo: "git.local/order-api", ImageName: "order-api"}
		payItem := model.DeployItem{Name: "pay-gateway", TreeNodeID: payNode, Repo: "git.local/pay-gateway", ImageName: "pay-gateway"}
		if err := tx.Create(&orderItem).Error; err != nil {
			return err
		}
		if err := tx.Create(&payItem).Error; err != nil {
			return err
		}
		if err := EnsureAll(tx); err != nil {
			return err
		}
		if _, err := Open(tx, payItem.ID, "2.2.0-dev", "开发", 0); err != nil {
			return err
		}
		devID, _ := clusterID(tx, "开发")
		prodID, _ := clusterID(tx, "生产")
		order := model.ReleaseOrder{ItemID: orderItem.ID, Tag: "1.8.4", Env: "生产", Status: "running"}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		stages := []model.ReleaseStage{
			{OrderID: order.ID, Name: "构建", Seq: 1, Status: "done"},
			{OrderID: order.ID, Name: "预发", Seq: 2, ClusterID: devID, Status: "done"},
			{OrderID: order.ID, Name: "生产", Seq: 3, ClusterID: prodID, Status: "pending"},
		}
		return tx.Create(&stages).Error
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
