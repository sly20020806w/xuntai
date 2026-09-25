package model

// 剧本只做串行。已发布的才能启动。执行实例用 current_step_index 标当前步。

type Playbook struct {
	ID          uint   `gorm:"primaryKey"`
	Code        string `gorm:"size:64;uniqueIndex"`
	Name        string `gorm:"size:128;not null"`
	Status      string `gorm:"size:32;not null"`
	InputSchema string `gorm:"type:text"`
}

func (Playbook) TableName() string { return "playbook" }

type PlaybookStep struct {
	ID           uint   `gorm:"primaryKey"`
	PlaybookID   uint   `gorm:"not null"`
	Seq          int    `gorm:"not null"`
	StepKey      string `gorm:"size:64;not null"`
	Kind         string `gorm:"size:32;not null"`
	InputMapping string `gorm:"type:text"`
	OnError      string `gorm:"size:16"`
}

func (PlaybookStep) TableName() string { return "playbook_step" }

type Run struct {
	ID               uint   `gorm:"primaryKey"`
	PlaybookID       uint   `gorm:"not null"`
	IdempotencyKey   string `gorm:"size:128;not null"`
	Status           string `gorm:"size:32;not null"`
	CurrentStepIndex int
	InputJSON        string `gorm:"type:text"`
	ContextJSON      string `gorm:"type:text"`
	TriggerUserID    uint
	TreeNodeID       uint
	Version          int
	Audit            string `gorm:"type:text"`
}

func (Run) TableName() string { return "run" }

type RunStep struct {
	ID         uint   `gorm:"primaryKey"`
	RunID      uint   `gorm:"not null"`
	StepKey    string `gorm:"size:64;not null"`
	Seq        int    `gorm:"not null"`
	Kind       string `gorm:"size:32;not null"`
	Status     string `gorm:"size:32;not null"`
	OnError    string `gorm:"size:16"`
	InputJSON  string `gorm:"type:text"`
	OutputJSON string `gorm:"type:text"`
	Error      string `gorm:"type:text"`
}

func (RunStep) TableName() string { return "run_step" }
