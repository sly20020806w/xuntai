package playbook

import (
	"fmt"

	"gorm.io/gorm"

	"xuntai/internal/model"
)

// Drive 在工单审批通过后创建执行实例。工单包只调用这里，不调用任务、集群或发布。
func Drive(db *gorm.DB, ticket model.TicketInstance, userID uint) (uint, error) {
	var tpl model.TicketTemplate
	if err := db.First(&tpl, ticket.TemplateID).Error; err != nil {
		return 0, err
	}
	code := ""
	switch tpl.Name {
	case "生产发布":
		code = "release.prod.single"
	case "生产回滚":
		code = "release.prod.rollback"
	case "建库", "建账号", "SQL变更":
		code = "db.change"
	default:
		return 0, nil
	}
	input := decodeObject(ticket.PayloadJSON)
	input["ticket_id"] = ticket.ID
	input["tree_node_id"] = ticket.TreeNodeID
	run, err := Start(db, code, userID, fmt.Sprintf("%d", ticket.ID), input, "")
	if err != nil {
		return 0, err
	}
	return run.ID, nil
}
