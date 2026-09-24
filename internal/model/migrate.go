package model

import "gorm.io/gorm"

// Models 是当前要建的表。数据库模块故意不在这里。
func Models() []any {
	return []any{
		&User{},
		&Role{},
		&Menu{},
		&API{},
		&Node{},
		&Machine{},
		&NodeOwner{},
		&TicketTemplate{},
		&TicketInstance{},
		&Script{},
		&Job{},
		&JobResult{},
		&ScrapePool{},
		&ScrapeJob{},
		&AlertmanagerCluster{},
		&DutyGroup{},
		&SendGroup{},
		&AlertRule{},
		&AlertEvent{},
		&Cluster{},
		&Project{},
		&App{},
		&AppInstance{},
		&DeployItem{},
		&ReleaseOrder{},
		&ReleaseStage{},
	}
}

func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(Models()...)
}
