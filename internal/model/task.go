package model

// 任务：脚本可复用。结果按任务和机器唯一，避免重复下发。

type Script struct {
	ID      uint   `gorm:"primaryKey"`
	Name    string `gorm:"size:128;not null"`
	Content string `gorm:"type:text"`
}

func (Script) TableName() string { return "task_scripts" }

type Job struct {
	ID         uint `gorm:"primaryKey"`
	ScriptID   uint `gorm:"not null"`
	TreeNodeID uint
	Name       string `gorm:"size:128;not null"`
	Status     string `gorm:"size:32"`
	BatchSize  int
}

func (Job) TableName() string { return "task_jobs" }

type JobResult struct {
	ID     uint   `gorm:"primaryKey"`
	JobID  uint   `gorm:"uniqueIndex:idx_job_host"`
	HostIP string `gorm:"size:64;uniqueIndex:idx_job_host"`
	Status string `gorm:"size:32"`
	Output string `gorm:"type:text"`
}

func (JobResult) TableName() string { return "task_results" }
