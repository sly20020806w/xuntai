package ticket

import (
	"xuntai/internal/base"
	"xuntai/internal/model"

	"gorm.io/gorm"
)

func APICatalog() []model.API {
	return []model.API{
		{Method: "GET", Path: "/api/ticket/templates"},
		{Method: "GET", Path: "/api/ticket/instances"},
		{Method: "POST", Path: "/api/ticket/instances"},
		{Method: "POST", Path: "/api/ticket/instances/:id/approve"},
		{Method: "POST", Path: "/api/ticket/instances/:id/reject"},
		{Method: "POST", Path: "/api/ticket/instances/:id/finish"},
	}
}

// Seed 补上工单接口。库里还没有工单时放入一张没有状态的模板和四张示例单。
func Seed(db *gorm.DB) (bool, error) {
	for _, role := range []string{"平台管理员", "节点运维"} {
		if err := base.Grant(db, role, APICatalog()); err != nil {
			return false, err
		}
	}
	var count int64
	if err := db.Model(&model.TicketInstance{}).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return false, ensureReleaseTemplates(db)
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		tpl := model.TicketTemplate{
			Name:     "变更申请",
			FormJSON: `{"fields":["标题","说明"]}`,
			FlowJSON: `{"steps":["approve","action"]}`,
		}
		if err := tx.Create(&tpl).Error; err != nil {
			return err
		}
		samples := []struct {
			title, node, user, status string
		}{
			{"订单库升配", "订单", "陈舟", "pending_approve"},
			{"支付证书轮换", "支付", "林夏", "pending_action"},
			{"入口扩容被拒绝", "入口", "周宁", "reject"},
			{"可观测节点初始化", "可观测", "许衡", "finished"},
		}
		for _, sample := range samples {
			nodeID, err := namedID(tx, &model.Node{}, sample.node)
			if err != nil {
				return err
			}
			userID, err := namedID(tx, &model.User{}, sample.user)
			if err != nil {
				return err
			}
			row := model.TicketInstance{
				TemplateID:  tpl.ID,
				TreeNodeID:  nodeID,
				Title:       sample.title,
				Status:      sample.status,
				ApplicantID: userID,
				CurrentNode: stepOf(sample.status),
				PayloadJSON: "{}",
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return true, ensureReleaseTemplates(db)
}

func ensureReleaseTemplates(db *gorm.DB) error {
	for _, name := range []string{"生产发布", "生产回滚"} {
		var count int64
		if err := db.Model(&model.TicketTemplate{}).Where("name = ?", name).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		row := model.TicketTemplate{
			Name:     name,
			FormJSON: `{"fields":["发布项","镜像标签","集群"]}`,
			FlowJSON: `{"steps":["approve","run"]}`,
		}
		if err := db.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func stepOf(status string) string {
	switch status {
	case "pending_approve":
		return "approve"
	case "pending_action":
		return "action"
	default:
		return ""
	}
}

func namedID(tx *gorm.DB, dest any, name string) (uint, error) {
	if err := tx.Where("name = ?", name).First(dest).Error; err != nil {
		return 0, err
	}
	switch row := dest.(type) {
	case *model.Node:
		return row.ID, nil
	case *model.User:
		return row.ID, nil
	default:
		return 0, gorm.ErrInvalidData
	}
}
