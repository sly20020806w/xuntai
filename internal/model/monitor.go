package model

// 监控：平台只存采集和告警配置。看图和存储不建表。

type ScrapePool struct {
	ID           uint   `gorm:"primaryKey"`
	Name         string `gorm:"size:128;not null"`
	RemoteWrite  string `gorm:"size:255"`
	SupportAlert bool
}

func (ScrapePool) TableName() string { return "monitor_scrape_pools" }

type ScrapeJob struct {
	ID          uint `gorm:"primaryKey"`
	PoolID      uint `gorm:"not null"`
	TreeNodeID  uint
	Name        string `gorm:"size:128;not null"`
	Discover    string `gorm:"size:16"` // tree 或 k8s
	Port        int
	MetricsPath string `gorm:"size:128"`
}

func (ScrapeJob) TableName() string { return "monitor_scrape_jobs" }

type AlertmanagerCluster struct {
	ID        uint   `gorm:"primaryKey"`
	Name      string `gorm:"size:128;not null"`
	Endpoints string `gorm:"size:512"`
}

func (AlertmanagerCluster) TableName() string { return "monitor_alertmanager_clusters" }

type DutyGroup struct {
	ID        uint   `gorm:"primaryKey"`
	Name      string `gorm:"size:128;not null"`
	ShiftDays int
}

func (DutyGroup) TableName() string { return "monitor_duty_groups" }

type SendGroup struct {
	ID          uint   `gorm:"primaryKey"`
	Name        string `gorm:"size:128;not null"`
	DutyGroupID uint
	ClusterID   uint
	TreeNodeID  uint
	AppID       uint
	ObjectID    uint
}

func (SendGroup) TableName() string { return "monitor_send_groups" }

type AlertRule struct {
	ID          uint   `gorm:"primaryKey"`
	Name        string `gorm:"size:128;not null"`
	Expr        string `gorm:"type:text"`
	Level       string `gorm:"size:32"`
	PoolID      uint   `gorm:"not null"`
	SendGroupID uint   `gorm:"not null"`
	TreeNodeID  uint
	AppID       uint
	ObjectID    uint
}

func (AlertRule) TableName() string { return "monitor_alert_rules" }

type AlertEvent struct {
	ID          uint `gorm:"primaryKey"`
	RuleID      uint
	Fingerprint string `gorm:"size:128"`
	Status      string `gorm:"size:32"`
}

func (AlertEvent) TableName() string { return "monitor_alert_events" }

// AlertAction 记下对某条告警的认领、屏蔽或升级。
type AlertAction struct {
	ID          uint   `gorm:"primaryKey"`
	Fingerprint string `gorm:"size:128;index"`
	TreeNodeID  uint
	ObjectID    uint
	Action      string `gorm:"size:16"`
	ActorID     uint
	OwnerID     uint
}

func (AlertAction) TableName() string { return "monitor_alert_actions" }
