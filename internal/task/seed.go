package task

import (
	"xuntai/internal/base"
	"xuntai/internal/model"

	"gorm.io/gorm"
)

func APICatalog() []model.API {
	return []model.API{
		{Method: "GET", Path: "/api/task/scripts"},
		{Method: "POST", Path: "/api/task/scripts"},
		{Method: "GET", Path: "/api/task/jobs"},
		{Method: "POST", Path: "/api/task/jobs"},
		{Method: "POST", Path: "/api/task/jobs/:id/pause"},
		{Method: "POST", Path: "/api/task/jobs/:id/resume"},
		{Method: "POST", Path: "/api/task/jobs/:id/results"},
	}
}

// Seed 补上任务接口。还没有任务时，放三条示例：执行中、暂停、已完成。
func Seed(db *gorm.DB) (bool, error) {
	for _, role := range []string{"平台管理员", "节点运维"} {
		if err := base.Grant(db, role, APICatalog()); err != nil {
			return false, err
		}
	}
	var count int64
	if err := db.Model(&model.Job{}).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		scripts := []model.Script{
			{Name: "核对时钟", Content: "date -u"},
			{Name: "内核参数基线", Content: "sysctl net.ipv4.ip_forward"},
			{Name: "磁盘只读巡检", Content: "基线：根分区必须可写\nfindmnt -n -o OPTIONS /"},
		}
		if err := tx.Create(&scripts).Error; err != nil {
			return err
		}
		byName := map[string]model.Script{}
		for _, script := range scripts {
			byName[script.Name] = script
		}
		clockNode, err := nodeID(tx, "可观测")
		if err != nil {
			return err
		}
		orderNode, err := nodeID(tx, "订单")
		if err != nil {
			return err
		}
		payNode, err := nodeID(tx, "支付")
		if err != nil {
			return err
		}
		clock, err := Open(tx, "核对时钟", byName["核对时钟"].ID, clockNode, 2, nil)
		if err != nil {
			return err
		}
		if err := reportFirstIssued(tx, clock.ID, "success", "时钟一致"); err != nil {
			return err
		}
		paused, err := Open(tx, "内核参数基线", byName["内核参数基线"].ID, orderNode, 1, nil)
		if err != nil {
			return err
		}
		if err := Pause(tx, paused.ID); err != nil {
			return err
		}
		if err := reportFirstIssued(tx, paused.ID, "success", "与基线一致"); err != nil {
			return err
		}
		inspect, err := Open(tx, "磁盘只读巡检", byName["磁盘只读巡检"].ID, payNode, 1, nil)
		if err != nil {
			return err
		}
		return reportFirstIssued(tx, inspect.ID, "success", "根分区可写")
	})
	return err == nil, err
}

func nodeID(tx *gorm.DB, name string) (uint, error) {
	var node model.Node
	if err := tx.Where("name = ?", name).First(&node).Error; err != nil {
		return 0, err
	}
	return node.ID, nil
}

func reportFirstIssued(tx *gorm.DB, jobID uint, status, output string) error {
	var row model.JobResult
	err := tx.Where("job_id = ? AND status = ?", jobID, "issued").Order("id").First(&row).Error
	if err != nil {
		return err
	}
	return Report(tx, jobID, row.HostIP, status, output)
}
