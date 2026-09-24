package cicd

import (
	"errors"

	"gorm.io/gorm"

	"xuntai/internal/model"
)

// Open 打开发布。开发环境不需要工单，提交后直接完成。
// 生产必须有一张已审批、且落在同一叶子上的工单，阶段停在构建。
func Open(db *gorm.DB, itemID uint, tag, env string, ticketID uint) (model.ReleaseOrder, error) {
	var order model.ReleaseOrder
	if tag == "" || (env != "开发" && env != "生产") {
		return order, ErrBadEnv
	}
	var item model.DeployItem
	if err := db.First(&item, itemID).Error; err != nil {
		return order, ErrNotFound
	}
	if env == "生产" {
		if ticketID == 0 {
			return order, ErrNeedTicket
		}
		var ticket model.TicketInstance
		if err := db.First(&ticket, ticketID).Error; err != nil {
			return order, ErrNotFound
		}
		if ticket.Status != "pending_action" {
			return order, ErrTicketState
		}
		if ticket.TreeNodeID != item.TreeNodeID {
			return order, ErrTicketNode
		}
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		order = model.ReleaseOrder{ItemID: item.ID, Tag: tag, Env: env, TicketID: ticketID}
		if env == "开发" {
			order.Status = "finished"
		} else {
			order.Status = "running"
		}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		if env == "开发" {
			devID, _ := clusterID(tx, "开发")
			stage := model.ReleaseStage{OrderID: order.ID, Name: "部署", Seq: 1, ClusterID: devID, Status: "done"}
			if err := tx.Create(&stage).Error; err != nil {
				return err
			}
			err := applyImage(tx, item, "开发", tag)
			if errors.Is(err, ErrNoInstance) {
				return nil
			}
			return err
		}
		preID, _ := clusterID(tx, "开发")
		prodID, _ := clusterID(tx, "生产")
		stages := []model.ReleaseStage{
			{OrderID: order.ID, Name: "构建", Seq: 1, Status: "pending"},
			{OrderID: order.ID, Name: "预发", Seq: 2, ClusterID: preID, Status: "pending"},
			{OrderID: order.ID, Name: "生产", Seq: 3, ClusterID: prodID, Status: "pending"},
		}
		return tx.Create(&stages).Error
	})
	return order, err
}

// Confirm 确认当前停着的阶段。生产阶段通过后，把标签写进对应实例。
func Confirm(db *gorm.DB, orderID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var order model.ReleaseOrder
		if err := tx.First(&order, orderID).Error; err != nil {
			return ErrNotFound
		}
		var stages []model.ReleaseStage
		if err := tx.Where("order_id = ?", order.ID).Order("seq").Find(&stages).Error; err != nil {
			return err
		}
		var current *model.ReleaseStage
		for i := range stages {
			if stages[i].Status == "pending" {
				current = &stages[i]
				break
			}
		}
		if current == nil {
			return ErrNoStage
		}
		if err := tx.Model(current).Update("status", "done").Error; err != nil {
			return err
		}
		var item model.DeployItem
		if err := tx.First(&item, order.ItemID).Error; err != nil {
			return err
		}
		switch current.Name {
		case "预发":
			err := applyImage(tx, item, "开发", order.Tag)
			if errors.Is(err, ErrNoInstance) {
				return nil
			}
			return err
		case "生产":
			if err := applyImage(tx, item, "生产", order.Tag); err != nil {
				return err
			}
			if err := tx.Model(&order).Update("status", "finished").Error; err != nil {
				return err
			}
			if order.TicketID != 0 {
				return tx.Model(&model.TicketInstance{}).Where("id = ?", order.TicketID).
					Updates(map[string]any{"status": "finished", "current_node": ""}).Error
			}
		}
		return nil
	})
}

func applyImage(db *gorm.DB, item model.DeployItem, env, tag string) error {
	var app model.App
	if err := db.Where("name = ?", item.Name).First(&app).Error; err != nil {
		return ErrNoInstance
	}
	var cluster model.Cluster
	if err := db.Where("env = ?", env).First(&cluster).Error; err != nil {
		return ErrNoInstance
	}
	res := db.Model(&model.AppInstance{}).
		Where("app_id = ? AND cluster_id = ?", app.ID, cluster.ID).
		Update("image", item.ImageName+":"+tag)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNoInstance
	}
	return nil
}

func clusterID(db *gorm.DB, env string) (uint, error) {
	var cluster model.Cluster
	if err := db.Where("env = ?", env).First(&cluster).Error; err != nil {
		return 0, err
	}
	return cluster.ID, nil
}
