package model

import "time"

// 工单：模板没有状态。状态只写在实例上，审批全部在执行前面。
// 四种状态保持不变。run_id 只是关联执行实例，工单自己不调用任务、集群或发布。

var TicketStatuses = []string{
	"pending_approve",
	"reject",
	"pending_action",
	"finished",
}

type TicketTemplate struct {
	ID       uint   `gorm:"primaryKey"`
	Name     string `gorm:"size:128;not null"`
	FormJSON string `gorm:"type:text"`
	FlowJSON string `gorm:"type:text"`
}

func (TicketTemplate) TableName() string { return "ticket_templates" }

type TicketInstance struct {
	ID          uint `gorm:"primaryKey"`
	TemplateID  uint `gorm:"not null"`
	TreeNodeID  uint
	Title       string `gorm:"size:255;not null"`
	Status      string `gorm:"size:32;not null"`
	ApplicantID uint
	CurrentNode string `gorm:"size:64"`
	PayloadJSON string `gorm:"type:text"`
	RunID       uint
	ApprovedBy  uint
	ApprovedAt  *time.Time
}

func (TicketInstance) TableName() string { return "ticket_instances" }
