package model

import "gorm.io/gorm"

// Models 是当前要建的表。
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
		&Instance{},
		&Backup{},
		&Restore{},
		&CMDBModel{},
		&CMDBObject{},
		&ObjectNode{},
		&Playbook{},
		&PlaybookStep{},
		&Run{},
		&RunStep{},
	}
}

func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(Models()...)
}
