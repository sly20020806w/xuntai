package playbook

import (
	"xuntai/internal/base"
	"xuntai/internal/model"

	"gorm.io/gorm"
)

func APICatalog() []model.API {
	return []model.API{
		{Method: "GET", Path: "/api/playbook/playbooks"},
		{Method: "GET", Path: "/api/playbook/playbooks/:id"},
		{Method: "GET", Path: "/api/playbook/runs"},
		{Method: "POST", Path: "/api/playbook/runs"},
		{Method: "GET", Path: "/api/playbook/runs/:id"},
		{Method: "POST", Path: "/api/playbook/runs/:id/continue"},
		{Method: "POST", Path: "/api/playbook/runs/:id/cancel"},
		{Method: "POST", Path: "/api/playbook/runs/:id/retry"},
		{Method: "POST", Path: "/api/playbook/runs/:id/sync"},
	}
}

// Seed 发布三套串行剧本，并补上巡检脚本。已有的同名剧本不覆盖。
func Seed(db *gorm.DB) (bool, error) {
	for _, role := range []string{"平台管理员", "节点运维"} {
		if err := base.Grant(db, role, APICatalog()); err != nil {
			return false, err
		}
	}
	var script model.Script
	if err := db.Where("name = ?", "主机基线巡检").Limit(1).Find(&script).Error; err != nil {
		return false, err
	}
	if script.ID == 0 {
		script = model.Script{Name: "主机基线巡检", Content: "基线核对：对照主机静态配置，不采集运行状态"}
		if err := db.Create(&script).Error; err != nil {
			return false, err
		}
	}
	created := false
	books := []struct {
		code, name, schema string
		steps              []model.PlaybookStep
	}{
		{
			code: "inspect.host.baseline", name: "主机基线巡检",
			schema: `{"required":["tree_node_id","host_ids"],"optional":["baseline_id"]}`,
			steps: []model.PlaybookStep{{
				Seq: 1, StepKey: "run_inspect", Kind: "task_run", OnError: "stop",
				InputMapping: `{"tree_node_id":"{{ run.input.tree_node_id }}","host_ids":"{{ run.input.host_ids }}"}`,
			}},
		},
		{
			code: "release.prod.single", name: "生产单次发布",
			schema: `{"required":["ticket_id","tree_node_id","release_item_id","image_tag","clusters"]}`,
			steps:  releaseSteps(`{"tree_node_id":"{{ run.input.tree_node_id }}","release_item_id":"{{ run.input.release_item_id }}","image_tag":"{{ run.input.image_tag }}","clusters":"{{ run.input.clusters }}"}`),
		},
		{
			code: "release.prod.rollback", name: "生产回滚",
			schema: `{"required":["ticket_id","tree_node_id","release_item_id","previous_tag","clusters"]}`,
			steps:  releaseSteps(`{"tree_node_id":"{{ run.input.tree_node_id }}","release_item_id":"{{ run.input.release_item_id }}","previous_tag":"{{ run.input.previous_tag }}","clusters":"{{ run.input.clusters }}"}`),
		},
	}
	for _, item := range books {
		var have model.Playbook
		if err := db.Where("code = ?", item.code).Limit(1).Find(&have).Error; err != nil {
			return false, err
		}
		if have.ID != 0 {
			continue
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			row := model.Playbook{Code: item.code, Name: item.name, Status: "published", InputSchema: item.schema}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			for i := range item.steps {
				item.steps[i].PlaybookID = row.ID
			}
			return tx.Create(&item.steps).Error
		})
		if err != nil {
			return false, err
		}
		created = true
	}
	return created, nil
}

func releaseSteps(deploy string) []model.PlaybookStep {
	return []model.PlaybookStep{
		{Seq: 1, StepKey: "gate", Kind: "ticket_gate", OnError: "stop", InputMapping: `{"ticket_id":"{{ run.input.ticket_id }}"}`},
		{Seq: 2, StepKey: "deploy", Kind: "k8s_apply", OnError: "stop", InputMapping: deploy},
		{Seq: 3, StepKey: "confirm", Kind: "wait_manual", OnError: "stop", InputMapping: `{}`},
	}
}
