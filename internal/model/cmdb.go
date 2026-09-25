package model

// CMDB 对象：模型、对象、对象和节点的关系放在三张表。
// 对象只记配置，不记运行状态。主机仍然走原来的机器表。

type CMDBModel struct {
	ID     uint   `gorm:"primaryKey"`
	Name   string `gorm:"size:128;not null"`
	Code   string `gorm:"size:64;uniqueIndex"`
	Remark string `gorm:"size:255"`
}

func (CMDBModel) TableName() string { return "model" }

type CMDBObject struct {
	ID         uint   `gorm:"primaryKey"`
	ModelID    uint   `gorm:"not null"`
	Name       string `gorm:"size:128;not null"`
	TreeNodeID uint
	AttrJSON   string `gorm:"type:text"`
}

func (CMDBObject) TableName() string { return "object" }

type ObjectNode struct {
	ID       uint `gorm:"primaryKey"`
	ObjectID uint `gorm:"uniqueIndex:idx_object_node"`
	NodeID   uint `gorm:"uniqueIndex:idx_object_node"`
}

func (ObjectNode) TableName() string { return "object_node" }
