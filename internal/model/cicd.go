package model

// 发布：发布项绑服务树叶子。开发环境不走工单，生产工单按阶段推进。

type DeployItem struct {
	ID         uint   `gorm:"primaryKey"`
	Name       string `gorm:"size:128;not null"`
	TreeNodeID uint
	Repo       string `gorm:"size:255"`
	ImageName  string `gorm:"size:255"`
}

func (DeployItem) TableName() string { return "cicd_deploy_items" }

type ReleaseOrder struct {
	ID     uint   `gorm:"primaryKey"`
	ItemID uint   `gorm:"not null"`
	Tag    string `gorm:"size:128"`
	Env    string `gorm:"size:32"`
	Status string `gorm:"size:32"`
}

func (ReleaseOrder) TableName() string { return "cicd_orders" }

type ReleaseStage struct {
	ID        uint   `gorm:"primaryKey"`
	OrderID   uint   `gorm:"not null"`
	Name      string `gorm:"size:64"`
	ClusterID uint
	Seq       int
	Status    string `gorm:"size:32"`
}

func (ReleaseStage) TableName() string { return "cicd_stages" }
