package task

import (
	"errors"
	"sort"

	"gorm.io/gorm"

	"xuntai/internal/model"
	"xuntai/internal/tree"
)

// Open 在一个节点下发同一条命令。每台机器一行结果，先只放出 batchSize 台。
func Open(db *gorm.DB, name string, scriptID, nodeID uint, batchSize int, hosts []string) (model.Job, error) {
	var job model.Job
	if batchSize < 1 || name == "" {
		return job, ErrBadStatus
	}
	var script model.Script
	if err := db.First(&script, scriptID).Error; err != nil {
		return job, ErrScript
	}
	var node model.Node
	if err := db.First(&node, nodeID).Error; err != nil {
		return job, ErrNode
	}
	ips, err := hostsUnder(db, nodeID, hosts)
	if err != nil {
		return job, err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		job = model.Job{
			ScriptID: script.ID, TreeNodeID: node.ID, Name: name,
			Status: "running", BatchSize: batchSize,
		}
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		rows := make([]model.JobResult, 0, len(ips))
		for i, ip := range ips {
			status := "pending"
			if i < batchSize {
				status = "issued"
			}
			rows = append(rows, model.JobResult{JobID: job.ID, HostIP: ip, Status: status})
		}
		return tx.Create(&rows).Error
	})
	return job, err
}

func Pause(db *gorm.DB, jobID uint) error {
	res := db.Model(&model.Job{}).Where("id = ? AND status = ?", jobID, "running").Update("status", "paused")
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotRunning
	}
	return nil
}

func Resume(db *gorm.DB, jobID uint) error {
	res := db.Model(&model.Job{}).Where("id = ? AND status = ?", jobID, "paused").Update("status", "running")
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotPaused
	}
	return advance(db, jobID)
}

// Report 把代理收回的结果写回同一行。已经发出去的才能收回，避免再插一条。
func Report(db *gorm.DB, jobID uint, host, status, output string) error {
	if status != "success" && status != "failed" {
		return ErrBadStatus
	}
	var row model.JobResult
	err := db.Where("job_id = ? AND host_ip = ?", jobID, host).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrUnknownHost
	}
	if err != nil {
		return err
	}
	if row.Status != "issued" {
		return ErrNotIssued
	}
	res := db.Model(&model.JobResult{}).Where("id = ? AND status = ?", row.ID, "issued").
		Updates(map[string]any{"status": status, "output": output})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotIssued
	}
	return advance(db, jobID)
}

func advance(db *gorm.DB, jobID uint) error {
	var job model.Job
	if err := db.First(&job, jobID).Error; err != nil {
		return err
	}
	if job.Status == "running" {
		var issued int64
		if err := db.Model(&model.JobResult{}).Where("job_id = ? AND status = ?", job.ID, "issued").Count(&issued).Error; err != nil {
			return err
		}
		room := job.BatchSize - int(issued)
		if room > 0 {
			var waiting []model.JobResult
			err := db.Where("job_id = ? AND status = ?", job.ID, "pending").Order("id").Limit(room).Find(&waiting).Error
			if err != nil {
				return err
			}
			for _, row := range waiting {
				if err := db.Model(&model.JobResult{}).Where("id = ? AND status = ?", row.ID, "pending").Update("status", "issued").Error; err != nil {
					return err
				}
			}
		}
	}
	var open int64
	err := db.Model(&model.JobResult{}).Where("job_id = ? AND status IN ?", jobID, []string{"pending", "issued"}).Count(&open).Error
	if err != nil {
		return err
	}
	if open == 0 {
		return db.Model(&model.Job{}).Where("id = ?", jobID).Update("status", "finished").Error
	}
	return nil
}

func hostsUnder(db *gorm.DB, nodeID uint, wanted []string) ([]string, error) {
	ids, err := tree.SubtreeIDs(db, nodeID)
	if err != nil {
		return nil, err
	}
	var machines []model.Machine
	err = db.Table("tree_machines").
		Select("DISTINCT tree_machines.id, tree_machines.name, tree_machines.ip, tree_machines.vendor, tree_machines.spec").
		Joins("JOIN tree_node_machines ON tree_node_machines.machine_id = tree_machines.id").
		Where("tree_node_machines.node_id IN ?", ids).
		Order("tree_machines.ip").
		Scan(&machines).Error
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, machine := range machines {
		if machine.IP != "" {
			allowed[machine.IP] = true
		}
	}
	ips := wanted
	if len(ips) == 0 {
		ips = make([]string, 0, len(allowed))
		for ip := range allowed {
			ips = append(ips, ip)
		}
		sort.Strings(ips)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		if seen[ip] {
			return nil, ErrDuplicate
		}
		seen[ip] = true
		if !allowed[ip] {
			return nil, ErrForeignHost
		}
		out = append(out, ip)
	}
	if len(out) == 0 {
		return nil, ErrNoMachines
	}
	return out, nil
}
