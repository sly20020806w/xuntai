package playbook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"xuntai/internal/cicd"
	"xuntai/internal/model"
	"xuntai/internal/monitor"
)

var activeStatus = []string{"pending", "running", "paused"}

func Start(db *gorm.DB, code string, userID uint, idemKey string, input map[string]any, contextJSON string) (model.Run, error) {
	var run model.Run
	idemKey = strings.TrimSpace(idemKey)
	if idemKey == "" {
		return run, fmt.Errorf("需要幂等键")
	}
	if input == nil {
		input = map[string]any{}
	}
	if strings.TrimSpace(contextJSON) == "" {
		contextJSON = "{}"
	}
	var book model.Playbook
	if err := db.Where("code = ?", code).First(&book).Error; err != nil {
		return run, ErrNotFound
	}
	if book.Status != "published" {
		return run, ErrNotPublished
	}
	if err := requireInput(code, input); err != nil {
		return run, err
	}
	nodeID, ok := asUint(input["tree_node_id"])
	if !ok {
		return run, fmt.Errorf("需要节点")
	}
	if err := mustWrite(db, userID, nodeID); err != nil {
		return run, err
	}
	var existing model.Run
	err := db.Where("playbook_id = ? AND idempotency_key = ? AND status IN ?", book.ID, idemKey, activeStatus).First(&existing).Error
	if err == nil {
		return existing, conflictError{RunID: existing.ID}
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return run, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return run, err
	}
	var templates []model.PlaybookStep
	if err := db.Where("playbook_id = ?", book.ID).Order("seq").Find(&templates).Error; err != nil {
		return run, err
	}
	if len(templates) == 0 {
		return run, fmt.Errorf("剧本没有步骤")
	}
	run = model.Run{
		PlaybookID: book.ID, IdempotencyKey: idemKey, Status: "pending",
		InputJSON: string(raw), ContextJSON: contextJSON, TriggerUserID: userID,
		TreeNodeID: nodeID, Version: 1,
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		steps := make([]model.RunStep, 0, len(templates)+1)
		for _, item := range templates {
			steps = append(steps, model.RunStep{
				RunID: run.ID, StepKey: item.StepKey, Seq: item.Seq, Kind: item.Kind,
				Status: "pending", OnError: item.OnError, InputJSON: "{}", OutputJSON: "{}",
			})
		}
		if code == "release.prod.single" || code == "release.prod.rollback" {
			itemID, _ := asUint(input["release_item_id"])
			spec, err := cicd.SpecFor(tx, itemID)
			if err != nil {
				return err
			}
			spec.Rollback = code == "release.prod.rollback"
			steps = materialize(steps, spec)
		}
		return tx.Create(&steps).Error
	})
	if err != nil {
		return run, err
	}
	if err := Advance(db, run.ID); err != nil {
		return run, err
	}
	if err := db.First(&run, run.ID).Error; err != nil {
		return run, err
	}
	return run, nil
}

func Advance(db *gorm.DB, runID uint) error {
	return advance(db, runID, 0)
}

func advance(db *gorm.DB, runID uint, depth int) error {
	if depth > 8 {
		return fmt.Errorf("步骤没有走完")
	}
	var run model.Run
	if err := db.First(&run, runID).Error; err != nil {
		return err
	}
	switch run.Status {
	case "success", "failed", "cancelled":
		return nil
	case "pending":
		if err := setRunStatus(db, run.ID, []string{"pending"}, "running"); err != nil {
			return err
		}
		return advance(db, runID, depth+1)
	case "paused":
		return nil
	}
	var steps []model.RunStep
	if err := db.Where("run_id = ?", run.ID).Order("seq").Find(&steps).Error; err != nil {
		return err
	}
	if run.CurrentStepIndex >= len(steps) {
		return finishRun(db, &run, "success")
	}
	step := steps[run.CurrentStepIndex]
	switch step.Status {
	case "success":
		return bump(db, &run, len(steps), depth)
	case "failed":
		if step.OnError == "ignore" {
			return bump(db, &run, len(steps), depth)
		}
		return finishRun(db, &run, "failed")
	case "waiting":
		return setRunStatus(db, run.ID, []string{"running"}, "paused")
	case "running":
		if step.Kind != "task_run" && step.Kind != "db_exec" {
			return nil
		}
		return dispatch(db, &run, &step, depth)
	default:
		return dispatch(db, &run, &step, depth)
	}
}

func dispatch(db *gorm.DB, run *model.Run, step *model.RunStep, depth int) error {
	input := decodeObject(run.InputJSON)
	context := decodeObject(run.ContextJSON)
	outputs, err := stepOutputs(db, run.ID)
	if err != nil {
		return err
	}
	mapping := step.InputJSON
	if mapping == "" || mapping == "{}" {
		var template model.PlaybookStep
		err := db.Where("playbook_id = ? AND step_key = ?", run.PlaybookID, step.StepKey).First(&template).Error
		if err == nil {
			mapping = template.InputMapping
		}
	}
	mapped, err := applyMapping(mapping, input, context, outputs)
	if err != nil {
		if err := writeStep(db, step, "failed", nil, err.Error()); err != nil {
			return err
		}
		return advance(db, run.ID, depth+1)
	}
	rawMapped, err := json.Marshal(mapped)
	if err != nil {
		return err
	}
	step.InputJSON = string(rawMapped)
	if err := db.Model(step).Update("input_json", step.InputJSON).Error; err != nil {
		return err
	}
	var output map[string]any
	status := "success"
	var stepErr error
	switch step.Kind {
	case "ticket_gate":
		ticketID, ok := asUint(mapped["ticket_id"])
		if !ok {
			stepErr = fmt.Errorf("需要工单")
			status = "failed"
			break
		}
		output, status, stepErr = gateTicket(db, ticketID)
	case "task_run":
		output, status, stepErr = runTask(db, run.TriggerUserID, mapped, *run, step)
	case "k8s_apply":
		output, status, stepErr = applyRelease(db, run, mapped)
	case "db_exec":
		output, status, stepErr = applyDB(db, *run, step, mapped)
	case "http_call":
		output, status, stepErr = callHTTP(mapped)
	case "wait_manual":
		status = "waiting"
	default:
		stepErr = fmt.Errorf("不认识的步骤")
		status = "failed"
	}
	if stepErr != nil && status != "failed" {
		status = "failed"
	}
	message := ""
	if stepErr != nil {
		message = stepErr.Error()
	}
	if status == "running" && output != nil {
		if err := writeStep(db, step, "running", output, ""); err != nil {
			return err
		}
		return setRunStatus(db, run.ID, activeStatus, "running")
	}
	if err := writeStep(db, step, status, output, message); err != nil {
		return err
	}
	return advance(db, run.ID, depth+1)
}

func Continue(db *gorm.DB, runID, stepID uint, version int, userID uint, extra map[string]any) error {
	var run model.Run
	if err := db.First(&run, runID).Error; err != nil {
		return ErrNotFound
	}
	if run.Status != "paused" {
		return ErrBadState
	}
	if run.Version != version {
		return ErrVersion
	}
	if err := mustWrite(db, userID, run.TreeNodeID); err != nil {
		return err
	}
	var steps []model.RunStep
	if err := db.Where("run_id = ?", run.ID).Order("seq").Find(&steps).Error; err != nil {
		return err
	}
	if run.CurrentStepIndex >= len(steps) || steps[run.CurrentStepIndex].ID != stepID {
		return ErrBadState
	}
	step := steps[run.CurrentStepIndex]
	if step.Status != "waiting" {
		return ErrBadState
	}
	res := db.Model(&model.Run{}).Where("id = ? AND status = ? AND version = ?", run.ID, "paused", version).
		Update("version", version+1)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrVersion
	}
	switch step.Kind {
	case "k8s_apply":
		if flagged(extra) {
			return failCanary(db, &run, &step)
		}
		if err := markContinued(db, &run, &step, userID, extra); err != nil {
			return err
		}
	case "wait_manual":
		if err := markContinued(db, &run, &step, userID, extra); err != nil {
			return err
		}
	case "ticket_gate":
		if err := db.Model(&model.RunStep{}).Where("id = ?", step.ID).Update("status", "pending").Error; err != nil {
			return err
		}
	default:
		return ErrBadState
	}
	if err := setRunStatus(db, run.ID, []string{"paused"}, "running"); err != nil {
		return err
	}
	return Advance(db, run.ID)
}

func Cancel(db *gorm.DB, runID uint) error {
	var run model.Run
	if err := db.First(&run, runID).Error; err != nil {
		return ErrNotFound
	}
	if run.Status != "running" && run.Status != "paused" {
		return ErrBadState
	}
	var steps []model.RunStep
	if err := db.Where("run_id = ?", run.ID).Order("seq").Find(&steps).Error; err != nil {
		return err
	}
	if run.CurrentStepIndex < len(steps) {
		step := steps[run.CurrentStepIndex]
		if step.Status == "pending" || step.Status == "running" || step.Status == "waiting" {
			if err := writeStep(db, &step, "failed", nil, "cancelled"); err != nil {
				return err
			}
		}
	}
	if err := setRunStatus(db, run.ID, []string{"running", "paused"}, "cancelled"); err != nil {
		return err
	}
	run.Status = "cancelled"
	if err := monitor.NoteRun(db, &run); err != nil {
		return err
	}
	return appendAudit(db, run.ID, "已取消")
}

func Retry(db *gorm.DB, runID, userID uint) (model.Run, error) {
	var old model.Run
	if err := db.First(&old, runID).Error; err != nil {
		return model.Run{}, ErrNotFound
	}
	if old.Status != "running" && old.Status != "paused" && old.Status != "failed" {
		return model.Run{}, ErrBadState
	}
	if err := mustWrite(db, userID, old.TreeNodeID); err != nil {
		return model.Run{}, err
	}
	var book model.Playbook
	if err := db.First(&book, old.PlaybookID).Error; err != nil {
		return model.Run{}, err
	}
	if old.Status == "running" || old.Status == "paused" {
		if err := Cancel(db, old.ID); err != nil {
			return model.Run{}, err
		}
	}
	if err := appendAudit(db, old.ID, "由重试取代"); err != nil {
		return model.Run{}, err
	}
	input := decodeObject(old.InputJSON)
	key, err := nextKey(db, old.PlaybookID, old.IdempotencyKey)
	if err != nil {
		return model.Run{}, err
	}
	return Start(db, book.Code, userID, key, input, old.ContextJSON)
}

func requireInput(code string, input map[string]any) error {
	var need []string
	switch code {
	case "inspect.host.baseline":
		need = []string{"tree_node_id", "host_ids"}
	case "release.prod.single":
		need = []string{"ticket_id", "tree_node_id", "release_item_id", "image_tag", "clusters"}
	case "release.prod.rollback":
		need = []string{"ticket_id", "tree_node_id", "release_item_id", "previous_tag", "clusters"}
	default:
		if _, ok := asUint(input["tree_node_id"]); !ok {
			return fmt.Errorf("需要节点")
		}
		return nil
	}
	for _, key := range need {
		if _, ok := input[key]; !ok {
			return fmt.Errorf("需要 %s", key)
		}
	}
	if code == "inspect.host.baseline" {
		if _, ok := asUintList(input["host_ids"]); !ok {
			return fmt.Errorf("需要主机")
		}
	}
	if code == "release.prod.single" || code == "release.prod.rollback" {
		if _, ok := asUintList(input["clusters"]); !ok {
			return fmt.Errorf("需要集群")
		}
	}
	return nil
}

func materialize(steps []model.RunStep, spec cicd.Spec) []model.RunStep {
	if spec.Executor == "rollouts" {
		return rolloutSteps(steps, spec)
	}
	batches := spec.Batches
	verify := spec.Verify
	out := make([]model.RunStep, 0, len(steps)+len(batches)+1)
	for _, step := range steps {
		if step.Kind == "wait_manual" && len(batches) >= 2 {
			for _, name := range batches {
				out = append(out, model.RunStep{
					RunID: step.RunID, StepKey: name, Kind: "wait_manual",
					Status: "pending", OnError: "stop", InputJSON: "{}", OutputJSON: "{}",
				})
			}
			continue
		}
		out = append(out, step)
	}
	if verify != "" {
		raw, err := json.Marshal(map[string]string{"url": verify})
		if err != nil {
			raw = []byte("{}")
		}
		runID := uint(0)
		if len(steps) > 0 {
			runID = steps[0].RunID
		}
		out = append(out, model.RunStep{
			RunID: runID, StepKey: "verify", Kind: "http_call",
			Status: "pending", OnError: "stop", InputJSON: string(raw), OutputJSON: "{}",
		})
	}
	for i := range out {
		out[i].Seq = i + 1
	}
	return out
}

func rolloutSteps(steps []model.RunStep, spec cicd.Spec) []model.RunStep {
	out := make([]model.RunStep, 0, len(steps)+4)
	runID := uint(0)
	if len(steps) > 0 {
		runID = steps[0].RunID
	}
	for _, step := range steps {
		if step.Kind == "k8s_apply" || step.Kind == "wait_manual" {
			continue
		}
		out = append(out, step)
	}
	if spec.Rollback {
		raw, _ := json.Marshal(map[string]any{"abort": true})
		out = append(out, model.RunStep{
			RunID: runID, StepKey: "abort", Kind: "k8s_apply",
			Status: "pending", OnError: "stop", InputJSON: string(raw), OutputJSON: "{}",
		})
	} else {
		seen := map[string]int{}
		for _, weight := range spec.Stages() {
			base := fmt.Sprintf("w%d", weight)
			seen[base]++
			key := base
			if seen[base] > 1 {
				key = fmt.Sprintf("%s_%d", base, seen[base])
			}
			raw, _ := json.Marshal(map[string]any{"weight": weight})
			out = append(out, model.RunStep{
				RunID: runID, StepKey: key, Kind: "k8s_apply",
				Status: "pending", OnError: "stop", InputJSON: string(raw), OutputJSON: "{}",
			})
		}
	}
	if spec.Verify != "" {
		raw, _ := json.Marshal(map[string]string{"url": spec.Verify})
		out = append(out, model.RunStep{
			RunID: runID, StepKey: "verify", Kind: "http_call",
			Status: "pending", OnError: "stop", InputJSON: string(raw), OutputJSON: "{}",
		})
	}
	for i := range out {
		out[i].Seq = i + 1
	}
	return out
}

func markContinued(db *gorm.DB, run *model.Run, step *model.RunStep, userID uint, extra map[string]any) error {
	context := decodeObject(run.ContextJSON)
	for key, value := range extra {
		context[key] = value
	}
	raw, err := json.Marshal(context)
	if err != nil {
		return err
	}
	if err := db.Model(run).Update("context_json", string(raw)).Error; err != nil {
		return err
	}
	var user model.User
	_ = db.First(&user, userID).Error
	output := map[string]any{
		"continued_by": user.Name,
		"continued_at": time.Now().UTC().Format(time.RFC3339),
		"form":         extra,
	}
	return writeStep(db, step, "success", output, "")
}

func flagged(extra map[string]any) bool {
	if extra == nil {
		return false
	}
	switch value := extra["failed"].(type) {
	case bool:
		return value
	case string:
		return value == "1" || value == "true"
	default:
		return false
	}
}

func bump(db *gorm.DB, run *model.Run, total, depth int) error {
	next := run.CurrentStepIndex + 1
	res := db.Model(&model.Run{}).Where("id = ? AND current_step_index = ?", run.ID, run.CurrentStepIndex).
		Update("current_step_index", next)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	if next >= total {
		var fresh model.Run
		if err := db.First(&fresh, run.ID).Error; err != nil {
			return err
		}
		return finishRun(db, &fresh, "success")
	}
	return advance(db, run.ID, depth+1)
}

func finishRun(db *gorm.DB, run *model.Run, status string) error {
	if err := setRunStatus(db, run.ID, []string{"running", "paused", "pending"}, status); err != nil {
		if errors.Is(err, ErrTerminal) {
			return nil
		}
		return err
	}
	run.Status = status
	if err := monitor.NoteRun(db, run); err != nil {
		return err
	}
	if status != "success" {
		return nil
	}
	input := decodeObject(run.InputJSON)
	ticketID, ok := asUint(input["ticket_id"])
	if !ok {
		return nil
	}
	return db.Model(&model.TicketInstance{}).Where("id = ? AND status = ?", ticketID, "pending_action").
		Updates(map[string]any{"status": "finished", "current_node": ""}).Error
}

func setRunStatus(db *gorm.DB, id uint, from []string, to string) error {
	res := db.Model(&model.Run{}).Where("id = ? AND status IN ?", id, from).Update("status", to)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrTerminal
	}
	return nil
}

func writeStep(db *gorm.DB, step *model.RunStep, status string, output map[string]any, message string) error {
	updates := map[string]any{"status": status, "error": message}
	if output != nil {
		raw, err := json.Marshal(output)
		if err != nil {
			return err
		}
		updates["output_json"] = string(raw)
		step.OutputJSON = string(raw)
	}
	step.Status = status
	step.Error = message
	return db.Model(&model.RunStep{}).Where("id = ?", step.ID).Updates(updates).Error
}

func stepOutputs(db *gorm.DB, runID uint) (map[string]map[string]any, error) {
	var steps []model.RunStep
	if err := db.Where("run_id = ?", runID).Find(&steps).Error; err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	for _, step := range steps {
		if step.Status != "success" {
			continue
		}
		out[step.StepKey] = decodeObject(step.OutputJSON)
	}
	return out, nil
}

func appendAudit(db *gorm.DB, runID uint, line string) error {
	var run model.Run
	if err := db.First(&run, runID).Error; err != nil {
		return err
	}
	audit := strings.TrimSpace(run.Audit)
	if audit != "" {
		audit += "\n"
	}
	audit += line
	return db.Model(&model.Run{}).Where("id = ?", runID).Update("audit", audit).Error
}

func nextKey(db *gorm.DB, playbookID uint, key string) (string, error) {
	base := key
	if i := strings.LastIndex(key, ":"); i > 0 {
		if _, err := fmt.Sscanf(key[i+1:], "%d", new(int)); err == nil {
			base = key[:i]
		}
	}
	var n int64
	err := db.Model(&model.Run{}).Where("playbook_id = ? AND (idempotency_key = ? OR idempotency_key LIKE ?)", playbookID, base, base+":%").Count(&n).Error
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d", base, n), nil
}
