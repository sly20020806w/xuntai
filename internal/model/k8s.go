package model

// 集群：集群记录放在平台库。节点状态不落表，直接向集群查。
// 应用侧是项目、应用、实例。实例才对应镜像和副本。

type Cluster struct {
	ID         uint   `gorm:"primaryKey"`
	Name       string `gorm:"size:128;not null"`
	Env        string `gorm:"size:32"`
	Kubeconfig string `gorm:"type:text"`
	Version    string `gorm:"size:32"`
	Health     string `gorm:"size:32"`
}

func (Cluster) TableName() string { return "k8s_clusters" }

type Project struct {
	ID         uint   `gorm:"primaryKey"`
	Name       string `gorm:"size:128;not null"`
	TreeNodeID uint
}

func (Project) TableName() string { return "k8s_projects" }

type App struct {
	ID        uint   `gorm:"primaryKey"`
	ProjectID uint   `gorm:"not null"`
	Name      string `gorm:"size:128;not null"`
}

func (App) TableName() string { return "k8s_apps" }

type AppInstance struct {
	ID        uint   `gorm:"primaryKey"`
	AppID     uint   `gorm:"not null"`
	ClusterID uint   `gorm:"not null"`
	Image     string `gorm:"size:255"`
	Replicas  int
}

func (AppInstance) TableName() string { return "k8s_instances" }
