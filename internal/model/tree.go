package model

// 服务树：只有叶子节点绑机器。负责人用于写操作鉴权。

type Node struct {
	ID       uint   `gorm:"primaryKey"`
	Name     string `gorm:"size:128;not null"`
	ParentID *uint
	IsLeaf   bool
	Level    int
}

func (Node) TableName() string { return "tree_nodes" }

type Machine struct {
	ID     uint   `gorm:"primaryKey"`
	Name   string `gorm:"size:128;not null"`
	IP     string `gorm:"size:64"`
	Vendor string `gorm:"size:32"`
	Spec   string `gorm:"size:64"`
	Nodes  []Node `gorm:"many2many:tree_node_machines;"`
}

func (Machine) TableName() string { return "tree_machines" }

type NodeOwner struct {
	ID     uint   `gorm:"primaryKey"`
	NodeID uint   `gorm:"uniqueIndex:idx_node_owner"`
	UserID uint   `gorm:"uniqueIndex:idx_node_owner"`
	Kind   string `gorm:"size:16;uniqueIndex:idx_node_owner"` // ops 或 rd
}

func (NodeOwner) TableName() string { return "tree_node_owners" }
