package playbook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"xuntai/internal/access"
	"xuntai/internal/cicd"
	"xuntai/internal/config"
	"xuntai/internal/model"
	"xuntai/internal/rollouts"
	"xuntai/internal/task"
)

// TaskMock 只在测试或本地打开。生产不设置时，巡检一直等到真实代理回写。
func TaskMock() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("XUNTAI_TASK_MOCK"))) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

func gateTicket(db *gorm.DB, ticketID uint) (map[string]any, string, error) {
	var ticket model.TicketInstance
	if err := db.First(&ticket, ticketID).Error; err != nil {
		return nil, "", fmt.Errorf("没有这张工单")
	}
	switch ticket.Status {
	case "pending_action", "finished":
		name := ""
		if ticket.ApprovedBy != 0 {
			var user model.User
			if err := db.First(&user, ticket.ApprovedBy).Error; err == nil {
				name = user.Name
			}
		}
		approvedAt := ""
		if ticket.ApprovedAt != nil {
			approvedAt = ticket.ApprovedAt.UTC().Format(time.RFC3339)
		}
		return map[string]any{
			"ticket_id":   ticket.ID,
			"approved_by": name,
			"approved_at": approvedAt,
		}, "success", nil
	case "pending_approve":
		return nil, "waiting", nil
	case "reject":
		return nil, "failed", fmt.Errorf("工单已驳回")
	default:
		return nil, "failed", fmt.Errorf("工单状态不能往下走")
	}
}

func runTask(db *gorm.DB, userID uint, mapped map[string]any, run model.Run, step *model.RunStep) (map[string]any, string, error) {
	nodeID, ok := asUint(mapped["tree_node_id"])
	if !ok {
		return nil, "failed", fmt.Errorf("需要节点")
	}
	if err := mustWrite(db, userID, nodeID); err != nil {
		return nil, "failed", err
	}
	hostIDs, ok := asUintList(mapped["host_ids"])
	if !ok {
		return nil, "failed", fmt.Errorf("需要主机")
	}
	previous := decodeObject(step.OutputJSON)
	if taskID, ok := asUint(previous["task_id"]); ok {
		return pollTask(db, taskID, hostIDs, run)
	}
	ips, _, err := hostIPs(db, hostIDs)
	if err != nil {
		return nil, "failed", err
	}
	var script model.Script
	if err := db.Where("name = ?", "主机基线巡检").First(&script).Error; err != nil {
		return nil, "failed", fmt.Errorf("没有巡检脚本")
	}
	job, err := task.Open(db, "主机基线巡检", script.ID, nodeID, len(ips), ips)
	if err != nil {
		return nil, "failed", taskText(err)
	}
	output := map[string]any{"task_id": job.ID}
	if id, ok := asUint(decodeObject(run.InputJSON)["baseline_id"]); ok {
		output["baseline_id"] = id
	}
	if err := writeStep(db, step, "running", output, ""); err != nil {
		return nil, "failed", err
	}
	return pollTask(db, job.ID, hostIDs, run)
}

func pollTask(db *gorm.DB, taskID uint, hostIDs []uint, run model.Run) (map[string]any, string, error) {
	if TaskMock() {
		if err := writeMockResults(db, taskID, run); err != nil {
			return nil, "failed", err
		}
	}
	var results []model.JobResult
	if err := db.Where("job_id = ?", taskID).Find(&results).Error; err != nil {
		return nil, "failed", err
	}
	byIP, err := ipsOf(db, hostIDs)
	if err != nil {
		return nil, "failed", err
	}
	successIDs := make([]any, 0)
	failedIDs := make([]any, 0)
	open := false
	summary := make([]string, 0, len(results))
	for _, row := range results {
		switch row.Status {
		case "pending", "issued":
			open = true
		case "success":
			if id, ok := byIP[row.HostIP]; ok {
				successIDs = append(successIDs, id)
			}
			if row.Output != "" {
				summary = append(summary, row.Output)
			}
		case "failed":
			if id, ok := byIP[row.HostIP]; ok {
				failedIDs = append(failedIDs, id)
			}
		}
	}
	output := map[string]any{
		"task_id":          taskID,
		"success_host_ids": successIDs,
		"failed_host_ids":  failedIDs,
		"outputs_uri":      fmt.Sprintf("/api/task/jobs/%d", taskID),
		"summary":          truncate(strings.Join(summary, "\n"), 512),
	}
	if id, ok := asUint(decodeObject(run.InputJSON)["baseline_id"]); ok {
		output["baseline_id"] = id
	}
	if open {
		return output, "running", nil
	}
	if len(failedIDs) > 0 {
		return output, "failed", fmt.Errorf("有主机没有通过巡检")
	}
	return output, "success", nil
}

func writeMockResults(db *gorm.DB, taskID uint, run model.Run) error {
	status := "success"
	output := "模拟巡检通过"
	if asString(decodeObject(run.InputJSON)["task_mock"]) == "failed" {
		status = "failed"
		output = "模拟巡检未通过"
	}
	for range 32 {
		var rows []model.JobResult
		if err := db.Where("job_id = ? AND status = ?", taskID, "issued").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if err := task.Report(db, taskID, row.HostIP, status, output); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("模拟回写没有收完")
}

func applyRelease(db *gorm.DB, run *model.Run, mapped map[string]any) (map[string]any, string, error) {
	merged := decodeObject(run.InputJSON)
	if weight, ok := mapped["weight"]; ok {
		merged["weight"] = weight
	}
	if abort, ok := mapped["abort"]; ok {
		merged["abort"] = abort
	}
	delete(merged, "executor")
	delete(merged, "weights")
	delete(merged, "strategy")
	delete(merged, "verify_url")
	mapped = merged
	userID := run.TriggerUserID
	nodeID, ok := asUint(mapped["tree_node_id"])
	if !ok {
		return nil, "failed", fmt.Errorf("需要节点")
	}
	if err := mustWrite(db, userID, nodeID); err != nil {
		return nil, "failed", err
	}
	itemID, ok := asUint(mapped["release_item_id"])
	if !ok {
		return nil, "failed", fmt.Errorf("需要发布项")
	}
	clusters, ok := asUintList(mapped["clusters"])
	if !ok {
		return nil, "failed", fmt.Errorf("需要集群")
	}
	tag := asString(mapped["image_tag"])
	if tag == "" {
		tag = asString(mapped["previous_tag"])
	}
	if tag == "" {
		return nil, "failed", fmt.Errorf("需要镜像标签")
	}
	var item model.DeployItem
	if err := db.First(&item, itemID).Error; err != nil {
		return nil, "failed", fmt.Errorf("没有这个发布项")
	}
	if item.TreeNodeID != nodeID {
		return nil, "failed", fmt.Errorf("发布项不在这个节点上")
	}
	if err := mustWrite(db, userID, item.TreeNodeID); err != nil {
		return nil, "failed", err
	}
	var cluster model.Cluster
	if err := db.First(&cluster, clusters[0]).Error; err != nil {
		return nil, "failed", fmt.Errorf("没有这个集群")
	}
	var app model.App
	if err := db.Where("name = ?", item.Name).First(&app).Error; err != nil {
		return nil, "failed", fmt.Errorf("没有对应的应用")
	}
	image := tag
	if item.ImageName != "" && !strings.Contains(tag, "/") && !strings.Contains(tag, ":") {
		image = item.ImageName + ":" + tag
	}
	var instance model.AppInstance
	err := db.Where("app_id = ? AND cluster_id = ?", app.ID, cluster.ID).First(&instance).Error
	if err != nil {
		return nil, "failed", fmt.Errorf("这个集群上没有实例")
	}
	attr, err := cicd.Load(db, item.ID)
	if err != nil {
		return nil, "failed", err
	}
	if cicd.ExecutorName(attr) == "rollouts" {
		return applyCanary(db, run, item.Name, cluster, instance, image, mapped, attr)
	}
	if cicd.ExecutorName(attr) != "platform" {
		return nil, "failed", cicd.ErrExecutor
	}
	res := db.Model(&model.AppInstance{}).Where("id = ?", instance.ID).Update("image", image)
	if res.Error != nil {
		return nil, "failed", res.Error
	}
	if res.RowsAffected == 0 {
		return nil, "failed", fmt.Errorf("镜像没有写上")
	}
	return map[string]any{
		"cluster":         cluster.ID,
		"app_instance_id": instance.ID,
		"revision":        instance.ID,
		"image_tag":       image,
		"executor":        "platform",
	}, "success", nil
}

func applyCanary(db *gorm.DB, run *model.Run, name string, cluster model.Cluster, instance model.AppInstance, image string, mapped map[string]any, attr cicd.Attr) (map[string]any, string, error) {
	ctx := decodeObject(run.ContextJSON)
	previous := asString(ctx["previous_image"])
	if previous == "" {
		previous = instance.Image
		ctx["previous_image"] = previous
		raw, err := json.Marshal(ctx)
		if err != nil {
			return nil, "failed", err
		}
		if err := db.Model(run).Update("context_json", string(raw)).Error; err != nil {
			return nil, "failed", err
		}
		run.ContextJSON = string(raw)
	}
	abort, _ := mapped["abort"].(bool)
	from := rollouts.Current(instance.ID).Weight
	weight := 100
	if !abort {
		got, ok := asUint(mapped["weight"])
		if !ok {
			return nil, "failed", fmt.Errorf("需要灰度百分比")
		}
		weight = int(got)
	}
	mode := "cluster"
	shown := weight
	if abort {
		shown = 0
	}
	if config.RolloutsMock() {
		mode = "mock"
		rollouts.Remember(instance.ID, rollouts.Stage{Weight: shown, Image: image, Previous: previous, Aborted: abort})
	} else {
		liveWeight := weight
		if abort {
			liveWeight = 100
		}
		if err := rollouts.Apply(cluster.Kubeconfig, "default", name, image, instance.Replicas, liveWeight, attr.StableSeconds); err != nil {
			return nil, "failed", err
		}
	}
	if abort || weight >= 100 {
		res := db.Model(&model.AppInstance{}).Where("id = ?", instance.ID).Update("image", image)
		if res.Error != nil {
			return nil, "failed", res.Error
		}
		if res.RowsAffected == 0 {
			return nil, "failed", fmt.Errorf("镜像没有写上")
		}
	}
	return map[string]any{
		"executor": "rollouts", "mode": mode, "weight": shown, "from": from,
		"target_image": image, "previous_image": previous, "stable_seconds": attr.StableSeconds,
		"aborted": abort, "cluster": cluster.ID, "app_instance_id": instance.ID,
	}, "waiting", nil
}

func failCanary(db *gorm.DB, run *model.Run, step *model.RunStep) error {
	input := decodeObject(run.InputJSON)
	itemID, _ := asUint(input["release_item_id"])
	attr, err := cicd.Load(db, itemID)
	if err != nil {
		return err
	}
	threshold := attr.FailureThreshold
	if threshold <= 0 {
		threshold = 1
	}
	ctx := decodeObject(run.ContextJSON)
	fails := 0
	if n, ok := asUint(ctx["rollout_failures"]); ok {
		fails = int(n)
	}
	fails++
	ctx["rollout_failures"] = fails
	raw, err := json.Marshal(ctx)
	if err != nil {
		return err
	}
	if err := db.Model(run).Update("context_json", string(raw)).Error; err != nil {
		return err
	}
	if fails < threshold {
		return nil
	}
	if attr.AutoRollback {
		prev := asString(ctx["previous_image"])
		out := decodeObject(step.OutputJSON)
		if id, ok := asUint(out["app_instance_id"]); ok && prev != "" {
			if err := db.Model(&model.AppInstance{}).Where("id = ?", id).Update("image", prev).Error; err != nil {
				return err
			}
			if config.RolloutsMock() {
				rollouts.Remember(id, rollouts.Stage{Weight: 0, Image: prev, Previous: prev, Aborted: true})
			}
		}
	}
	if err := writeStep(db, step, "failed", map[string]any{"rolled_back": attr.AutoRollback}, "灰度没有通过"); err != nil {
		return err
	}
	return finishRun(db, run, "failed")
}

func callHTTP(mapped map[string]any) (map[string]any, string, error) {
	url := asString(mapped["url"])
	if url == "" {
		return nil, "failed", fmt.Errorf("需要校验地址")
	}
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get(url)
	if err != nil {
		return nil, "failed", fmt.Errorf("校验地址没有通")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 513))
	if err != nil {
		return nil, "failed", fmt.Errorf("校验结果没有读出来")
	}
	output := map[string]any{
		"status_code": res.StatusCode,
		"body":        truncate(string(body), 512),
	}
	if res.StatusCode >= 400 {
		return output, "failed", fmt.Errorf("校验返回 %d", res.StatusCode)
	}
	return output, "success", nil
}

func mustWrite(db *gorm.DB, userID, nodeID uint) error {
	err := access.Allow(db, userID, nodeID)
	if err == nil {
		return nil
	}
	if errors.Is(err, access.ErrDenied) || errors.Is(err, access.ErrUnmounted) {
		return ErrForbidden
	}
	if errors.Is(err, access.ErrNodeAbsent) {
		return fmt.Errorf("没有这个节点")
	}
	return err
}

func hostIPs(db *gorm.DB, ids []uint) ([]string, map[string]uint, error) {
	byIP, err := ipsOf(db, ids)
	if err != nil {
		return nil, nil, err
	}
	ips := make([]string, 0, len(ids))
	seen := map[uint]bool{}
	for _, id := range ids {
		found := ""
		for ip, hostID := range byIP {
			if hostID == id {
				found = ip
				break
			}
		}
		if found == "" || seen[id] {
			return nil, nil, fmt.Errorf("没有这台机器")
		}
		seen[id] = true
		ips = append(ips, found)
	}
	return ips, byIP, nil
}

func ipsOf(db *gorm.DB, ids []uint) (map[string]uint, error) {
	var machines []model.Machine
	if err := db.Where("id IN ?", ids).Find(&machines).Error; err != nil {
		return nil, err
	}
	if len(machines) != len(ids) {
		return nil, fmt.Errorf("没有这台机器")
	}
	byIP := make(map[string]uint, len(machines))
	for _, machine := range machines {
		if machine.IP == "" {
			return nil, fmt.Errorf("机器没有地址")
		}
		byIP[machine.IP] = machine.ID
	}
	return byIP, nil
}

func taskText(err error) error {
	switch {
	case errors.Is(err, task.ErrForeignHost):
		return fmt.Errorf("有主机不在这个节点下")
	case errors.Is(err, task.ErrDuplicate):
		return fmt.Errorf("主机重复了")
	case errors.Is(err, task.ErrNoMachines):
		return fmt.Errorf("这个节点下没有机器")
	default:
		return err
	}
}

func truncate(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return text[:n]
}
