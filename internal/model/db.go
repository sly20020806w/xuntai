package model

// 数据库：只登记实例、主从关系和备份。复制延迟不落表，口令不入库。
// 还原要已审批的工单。不执行备份程序，也不把 MySQL 放进集群。

type Instance struct {
	ID          uint   `gorm:"primaryKey"`
	Name        string `gorm:"size:128;not null"`
	TreeNodeID  uint   `gorm:"not null"`
	Host        string `gorm:"size:64;uniqueIndex:idx_db_host_port"`
	Port        int    `gorm:"uniqueIndex:idx_db_host_port"`
	Version     string `gorm:"size:32"`
	Role        string `gorm:"size:16"` // 主 或 从
	MasterID    uint
	ObjectID    uint
	Env         string `gorm:"size:16"` // 开发、测试或生产。空的按生产
	LoginUser   string `gorm:"size:64"` // 连接账号，不存口令
	MonitorAddr string `gorm:"size:128"`
}

func (Instance) TableName() string { return "db_instances" }

type Backup struct {
	ID         uint   `gorm:"primaryKey"`
	InstanceID uint   `gorm:"not null"`
	Kind       string `gorm:"size:16"` // 全量 或 增量
	Keep       int
	Status     string `gorm:"size:32"`
}

func (Backup) TableName() string { return "db_backups" }

type Restore struct {
	ID         uint   `gorm:"primaryKey"`
	BackupID   uint   `gorm:"not null"`
	InstanceID uint   `gorm:"not null"`
	TicketID   uint   `gorm:"not null"`
	Status     string `gorm:"size:32"`
}

func (Restore) TableName() string { return "db_restores" }
