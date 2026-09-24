package monitor

import (
	"fmt"

	"gorm.io/gorm"

	"xuntai/internal/model"
	"xuntai/internal/tree"
)

type TargetJob struct {
	Name        string   `json:"name"`
	Discover    string   `json:"discover"`
	MetricsPath string   `json:"metricsPath"`
	Targets     []string `json:"targets"`
}

type PullFile struct {
	Pool        string      `json:"pool"`
	RemoteWrite string      `json:"remoteWrite"`
	Jobs        []TargetJob `json:"jobs"`
}

// Pull 是采集端来拉的配置。平台不往 Prometheus 推。
func Pull(db *gorm.DB, poolID uint) (PullFile, error) {
	var file PullFile
	var pool model.ScrapePool
	if err := db.First(&pool, poolID).Error; err != nil {
		return file, err
	}
	file.Pool = pool.Name
	file.RemoteWrite = pool.RemoteWrite
	var jobs []model.ScrapeJob
	if err := db.Where("pool_id = ?", pool.ID).Order("id").Find(&jobs).Error; err != nil {
		return file, err
	}
	file.Jobs = make([]TargetJob, 0, len(jobs))
	for _, job := range jobs {
		item := TargetJob{
			Name: job.Name, Discover: job.Discover, MetricsPath: job.MetricsPath,
			Targets: []string{},
		}
		if job.Discover == "tree" {
			ips, err := leafIPs(db, job.TreeNodeID)
			if err != nil {
				return file, err
			}
			for _, ip := range ips {
				item.Targets = append(item.Targets, fmt.Sprintf("%s:%d", ip, job.Port))
			}
		}
		file.Jobs = append(file.Jobs, item)
	}
	return file, nil
}

func leafIPs(db *gorm.DB, nodeID uint) ([]string, error) {
	if nodeID == 0 {
		return nil, nil
	}
	ids, err := tree.SubtreeIDs(db, nodeID)
	if err != nil {
		return nil, err
	}
	var machines []model.Machine
	err = db.Table("tree_machines").
		Select("DISTINCT tree_machines.ip").
		Joins("JOIN tree_node_machines ON tree_node_machines.machine_id = tree_machines.id").
		Where("tree_node_machines.node_id IN ? AND tree_machines.ip <> ''", ids).
		Order("tree_machines.ip").
		Scan(&machines).Error
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(machines))
	for _, machine := range machines {
		out = append(out, machine.IP)
	}
	return out, nil
}
