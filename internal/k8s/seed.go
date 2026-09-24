package k8s

import (
	"xuntai/internal/base"
	"xuntai/internal/model"

	"gorm.io/gorm"
)

func AdminAPIs() []model.API {
	return append(OpsAPIs(), model.API{Method: "POST", Path: "/api/k8s/clusters"})
}

func OpsAPIs() []model.API {
	return []model.API{
		{Method: "GET", Path: "/api/k8s/clusters"},
		{Method: "GET", Path: "/api/k8s/clusters/:id/nodes"},
		{Method: "GET", Path: "/api/k8s/projects"},
		{Method: "POST", Path: "/api/k8s/projects"},
		{Method: "POST", Path: "/api/k8s/apps"},
		{Method: "GET", Path: "/api/k8s/instances"},
		{Method: "POST", Path: "/api/k8s/instances"},
		{Method: "PUT", Path: "/api/k8s/instances/:id"},
	}
}

// Seed 补上集群接口。还没有集群时登记开发和生产，并放上订单、支付的实例。
// 不写 kubeconfig，节点状态留到真正连上集群再查。
func Seed(db *gorm.DB) (bool, error) {
	if err := base.Grant(db, "平台管理员", AdminAPIs()); err != nil {
		return false, err
	}
	if err := base.Grant(db, "节点运维", OpsAPIs()); err != nil {
		return false, err
	}
	var count int64
	if err := db.Model(&model.Cluster{}).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		prod := model.Cluster{Name: "prod-a", Env: "生产", Version: "1.29"}
		dev := model.Cluster{Name: "dev-a", Env: "开发", Version: "1.29"}
		if err := tx.Create(&prod).Error; err != nil {
			return err
		}
		if err := tx.Create(&dev).Error; err != nil {
			return err
		}
		orderNode, err := nodeID(tx, "订单")
		if err != nil {
			return err
		}
		payNode, err := nodeID(tx, "支付")
		if err != nil {
			return err
		}
		orderProject := model.Project{Name: "订单", TreeNodeID: orderNode}
		payProject := model.Project{Name: "支付", TreeNodeID: payNode}
		if err := tx.Create(&orderProject).Error; err != nil {
			return err
		}
		if err := tx.Create(&payProject).Error; err != nil {
			return err
		}
		orderApp := model.App{ProjectID: orderProject.ID, Name: "order-api"}
		payApp := model.App{ProjectID: payProject.ID, Name: "pay-gateway"}
		if err := tx.Create(&orderApp).Error; err != nil {
			return err
		}
		if err := tx.Create(&payApp).Error; err != nil {
			return err
		}
		instances := []model.AppInstance{
			{AppID: orderApp.ID, ClusterID: prod.ID, Image: "order-api:1.8.3", Replicas: 6},
			{AppID: orderApp.ID, ClusterID: dev.ID, Image: "order-api:1.8.3-dev", Replicas: 1},
			{AppID: payApp.ID, ClusterID: prod.ID, Image: "pay-gateway:2.1.0", Replicas: 4},
		}
		return tx.Create(&instances).Error
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
