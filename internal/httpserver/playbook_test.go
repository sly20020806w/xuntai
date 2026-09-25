package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"xuntai/internal/access"
	apibase "xuntai/internal/api/base"
	apicicd "xuntai/internal/api/cicd"
	apicmdb "xuntai/internal/api/cmdb"
	apik8s "xuntai/internal/api/k8s"
	apiplay "xuntai/internal/api/playbook"
	apitask "xuntai/internal/api/task"
	apiticket "xuntai/internal/api/ticket"
	apitree "xuntai/internal/api/tree"
	"xuntai/internal/base"
	"xuntai/internal/cicd"
	"xuntai/internal/cmdb"
	"xuntai/internal/k8s"
	"xuntai/internal/model"
	"xuntai/internal/playbook"
	"xuntai/internal/task"
	"xuntai/internal/ticket"
	"xuntai/internal/tree"
)

func TestCMDBObjectsKeepHostBinding(t *testing.T) {
	engine, db := playEngine(t)
	lin := loginName(t, engine, "林夏", "secret")
	zhou := loginName(t, engine, "周宁", "secret")
	xu := loginName(t, engine, "许衡", "secret")
	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	before := decodeJSON[[]struct {
		ID uint `json:"id"`
	}](t, getAuth(engine, "/api/tree/machines?nodeId="+fmt.Sprint(order.ID), lin))
	if len(before) < 2 {
		t.Fatalf("订单机器 = %d", len(before))
	}

	created := postJSON(engine, "/api/cmdb/models", `{"name":"配置项","code":"ci","remark":"静态"}`, zhou)
	if created.Code != http.StatusOK {
		t.Fatalf("建模型 = %d %s", created.Code, created.Body.String())
	}
	modelID := decodeJSON[struct {
		ID uint `json:"id"`
	}](t, created).ID
	object := postJSON(engine, "/api/cmdb/objects", fmt.Sprintf(`{"modelId":%d,"name":"订单配置","treeNodeId":%d,"attr":{"owner":"交易"}}`, modelID, order.ID), lin)
	if object.Code != http.StatusOK {
		t.Fatalf("建对象 = %d %s", object.Code, object.Body.String())
	}
	objectID := decodeJSON[struct {
		ID uint `json:"id"`
	}](t, object).ID
	if rec := putJSON(engine, fmt.Sprintf("/api/cmdb/objects/%d", objectID), `{"name":"订单配置已改","attr":{"owner":"交易"}}`, lin); rec.Code != http.StatusOK {
		t.Fatalf("改对象 = %d %s", rec.Code, rec.Body.String())
	}
	link := postJSON(engine, "/api/cmdb/object-nodes", fmt.Sprintf(`{"objectId":%d,"nodeId":%d}`, objectID, order.ID), lin)
	if link.Code != http.StatusOK {
		t.Fatalf("挂节点 = %d %s", link.Code, link.Body.String())
	}
	hidden := decodeJSON[[]struct {
		Name string `json:"name"`
	}](t, getAuth(engine, "/api/cmdb/objects", xu))
	for _, row := range hidden {
		if row.Name == "订单配置已改" {
			t.Fatal("许衡看见了订单对象")
		}
	}
	if rec := postJSON(engine, "/api/cmdb/objects", fmt.Sprintf(`{"modelId":%d,"name":"越权","treeNodeId":%d,"attr":{"status":"running"}}`, modelID, order.ID), xu); rec.Code != http.StatusForbidden {
		t.Fatalf("许衡建对象 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/cmdb/objects", fmt.Sprintf(`{"modelId":%d,"name":"带状态","treeNodeId":%d,"attr":{"health":"up"}}`, modelID, order.ID), lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("运行状态 = %d %s", rec.Code, rec.Body.String())
	}
	after := decodeJSON[[]struct {
		ID uint `json:"id"`
	}](t, getAuth(engine, "/api/tree/machines?nodeId="+fmt.Sprint(order.ID), lin))
	if len(after) != len(before) {
		t.Fatalf("机器数量从 %d 变成 %d", len(before), len(after))
	}
	if rec := deleteJSON(engine, fmt.Sprintf("/api/cmdb/objects/%d", objectID), lin); rec.Code != http.StatusOK {
		t.Fatalf("删对象 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := deleteJSON(engine, fmt.Sprintf("/api/cmdb/models/%d", modelID), zhou); rec.Code != http.StatusOK {
		t.Fatalf("删模型 = %d %s", rec.Code, rec.Body.String())
	}
	_ = db
}

func TestSerialPlaybookTicketAndIdempotency(t *testing.T) {
	t.Setenv("XUNTAI_TASK_MOCK", "")
	engine, db := playEngine(t)
	lin := loginName(t, engine, "林夏", "secret")
	xu := loginName(t, engine, "许衡", "secret")
	chen := loginName(t, engine, "陈舟", "secret")
	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	books := decodeJSON[[]struct {
		Code   string `json:"code"`
		Status string `json:"status"`
	}](t, getAuth(engine, "/api/playbook/playbooks", lin))
	seen := map[string]bool{}
	for _, book := range books {
		if book.Status == "published" {
			seen[book.Code] = true
		}
	}
	for _, code := range []string{"inspect.host.baseline", "release.prod.single", "release.prod.rollback"} {
		if !seen[code] {
			t.Fatalf("剧本 %s 没有发布", code)
		}
	}

	machines := decodeJSON[[]struct {
		ID     uint   `json:"id"`
		IP     string `json:"ip"`
		NodeID uint   `json:"nodeId"`
	}](t, getAuth(engine, fmt.Sprintf("/api/tree/machines?nodeId=%d", order.ID), lin))
	if len(machines) == 0 {
		t.Fatal("没有订单机器")
	}
	hostIDs := make([]uint, 0, len(machines))
	for _, machine := range machines {
		hostIDs = append(hostIDs, machine.ID)
	}
	hostJSON, _ := json.Marshal(hostIDs)
	if rec := postJSON(engine, "/api/playbook/runs", fmt.Sprintf(`{"playbook":"inspect.host.baseline","idempotencyKey":"inspect-1","input":{"tree_node_id":%d,"host_ids":%s}}`, order.ID, hostJSON), xu); rec.Code != http.StatusForbidden {
		t.Fatalf("许衡巡检 = %d %s", rec.Code, rec.Body.String())
	}
	started := postJSON(engine, "/api/playbook/runs", fmt.Sprintf(`{"playbook":"inspect.host.baseline","idempotencyKey":"inspect-1","input":{"tree_node_id":%d,"host_ids":%s,"baseline_id":7}}`, order.ID, hostJSON), lin)
	if started.Code != http.StatusOK {
		t.Fatalf("巡检 = %d %s", started.Code, started.Body.String())
	}
	run := decodeRun(t, started)
	if run.Status != "running" {
		t.Fatalf("巡检状态 = %s", run.Status)
	}
	again := postJSON(engine, "/api/playbook/runs", fmt.Sprintf(`{"playbook":"inspect.host.baseline","idempotencyKey":"inspect-1","input":{"tree_node_id":%d,"host_ids":%s}}`, order.ID, hostJSON), lin)
	if again.Code != http.StatusConflict {
		t.Fatalf("重复执行 = %d %s", again.Code, again.Body.String())
	}
	taskID := uint(0)
	for _, step := range run.Steps {
		if step.Key == "run_inspect" {
			taskID = uint(step.Output["task_id"].(float64))
		}
	}
	if taskID == 0 {
		t.Fatal("没有创建巡检任务")
	}
	for _, machine := range machines {
		rec := postJSON(engine, fmt.Sprintf("/api/task/jobs/%d/results", taskID), fmt.Sprintf(`{"hostIp":"%s","status":"success","output":"基线通过"}`, machine.IP), lin)
		if rec.Code != http.StatusOK {
			t.Fatalf("回写 %s = %d %s", machine.IP, rec.Code, rec.Body.String())
		}
	}
	synced := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/sync", run.ID), "", lin)
	if synced.Code != http.StatusOK {
		t.Fatalf("同步 = %d %s", synced.Code, synced.Body.String())
	}
	done := decodeRun(t, synced)
	if done.Status != "success" {
		t.Fatalf("巡检结束 = %s %+v", done.Status, done.Steps)
	}
	third := postJSON(engine, "/api/playbook/runs", fmt.Sprintf(`{"playbook":"inspect.host.baseline","idempotencyKey":"inspect-1","input":{"tree_node_id":%d,"host_ids":%s}}`, order.ID, hostJSON), lin)
	if third.Code != http.StatusOK {
		t.Fatalf("终态后同键再执行 = %d %s", third.Code, third.Body.String())
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/cancel", decodeRun(t, third).ID), "", lin); rec.Code != http.StatusOK {
		t.Fatalf("取消 = %d %s", rec.Code, rec.Body.String())
	}

	items := decodeJSON[[]namedID](t, getAuth(engine, "/api/cicd/items", lin))
	var itemID uint
	for _, item := range items {
		if item.Name == "order-api" {
			itemID = item.ID
		}
	}
	clusters := decodeJSON[[]struct {
		ID  uint   `json:"id"`
		Env string `json:"env"`
	}](t, getAuth(engine, "/api/k8s/clusters", lin))
	var clusterID uint
	for _, cluster := range clusters {
		if cluster.Env == "生产" {
			clusterID = cluster.ID
		}
	}
	templates := decodeJSON[[]namedID](t, getAuth(engine, "/api/ticket/templates", chen))
	var releaseTpl uint
	for _, tpl := range templates {
		if tpl.Name == "生产发布" {
			releaseTpl = tpl.ID
		}
	}
	verify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer verify.Close()
	if rec := putJSON(engine, fmt.Sprintf("/api/cicd/items/%d", itemID), fmt.Sprintf(`{"verifyUrl":%q}`, verify.URL), lin); rec.Code != http.StatusOK {
		t.Fatalf("写入校验地址 = %d %s", rec.Code, rec.Body.String())
	}
	payload := fmt.Sprintf(`{"release_item_id":%d,"image_tag":"1.9.0","clusters":[%d]}`, itemID, clusterID)
	created := postJSON(engine, "/api/ticket/instances", fmt.Sprintf(`{"templateId":%d,"treeNodeId":%d,"title":"订单生产发布","payload":%q}`, releaseTpl, order.ID, payload), chen)
	if created.Code != http.StatusOK {
		t.Fatalf("提单 = %d %s", created.Code, created.Body.String())
	}
	ticketID := decodeJSON[struct {
		ID uint `json:"id"`
	}](t, created).ID
	approved := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", ticketID), "", lin)
	if approved.Code != http.StatusOK {
		t.Fatalf("审批 = %d %s", approved.Code, approved.Body.String())
	}
	release := decodeJSON[struct {
		RunID uint `json:"runId"`
	}](t, approved)
	if release.RunID == 0 {
		t.Fatal("审批没有创建执行")
	}
	view := decodeRun(t, getAuth(engine, fmt.Sprintf("/api/playbook/runs/%d", release.RunID), lin))
	if view.Status != "paused" {
		t.Fatalf("发布状态 = %s %+v", view.Status, view.Steps)
	}
	if imageOf(t, engine, lin, "order-api", "生产") != "order-api:1.9.0" {
		t.Fatal("审批后的执行没有把镜像写上")
	}
	var confirmID uint
	for _, step := range view.Steps {
		if step.Key == "confirm" && step.Status == "waiting" {
			confirmID = step.ID
		}
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/continue", view.ID), fmt.Sprintf(`{"stepId":%d,"version":99}`, confirmID), lin); rec.Code != http.StatusConflict {
		t.Fatalf("错误版本 = %d %s", rec.Code, rec.Body.String())
	}
	continued := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/continue", view.ID), fmt.Sprintf(`{"stepId":%d,"version":%d,"continueInput":{"note":"观察通过"}}`, confirmID, view.Version), lin)
	if continued.Code != http.StatusOK {
		t.Fatalf("继续 = %d %s", continued.Code, continued.Body.String())
	}
	finalRun := decodeRun(t, continued)
	if finalRun.Status != "success" {
		t.Fatalf("发布结束 = %s %+v", finalRun.Status, finalRun.Steps)
	}
	verified := false
	for _, step := range finalRun.Steps {
		if step.Key == "verify" && step.Status == "success" {
			verified = true
		}
	}
	if !verified {
		t.Fatalf("校验没有从发布项读到 %+v", finalRun.Steps)
	}
	var ticketRow model.TicketInstance
	if err := db.First(&ticketRow, ticketID).Error; err != nil {
		t.Fatal(err)
	}
	if ticketRow.Status != "finished" || ticketRow.RunID != release.RunID {
		t.Fatalf("工单 = %+v", ticketRow)
	}

	dup := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", ticketID), "", lin)
	if dup.Code == http.StatusOK {
		t.Fatal("已结束的工单还能再审批")
	}

	rollbackTicket := postJSON(engine, "/api/ticket/instances", fmt.Sprintf(`{"templateId":%d,"treeNodeId":%d,"title":"只驳回","payload":"{}"}`, releaseTpl, order.ID), chen)
	rollbackID := decodeJSON[struct {
		ID uint `json:"id"`
	}](t, rollbackTicket).ID
	manual := postJSON(engine, "/api/playbook/runs", fmt.Sprintf(`{"playbook":"release.prod.single","idempotencyKey":"gate-%d","input":{"ticket_id":%d,"tree_node_id":%d,"release_item_id":%d,"image_tag":"1.9.1","clusters":[%d]}}`, rollbackID, rollbackID, order.ID, itemID, clusterID), lin)
	if manual.Code != http.StatusOK {
		t.Fatalf("未审批启动 = %d %s", manual.Code, manual.Body.String())
	}
	waiting := decodeRun(t, manual)
	if waiting.Status != "paused" || waiting.Steps[0].Status != "waiting" {
		t.Fatalf("关卡 = %s %+v", waiting.Status, waiting.Steps)
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/reject", rollbackID), "", lin); rec.Code != http.StatusOK {
		t.Fatalf("驳回 = %d %s", rec.Code, rec.Body.String())
	}
	rejected := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/continue", waiting.ID), fmt.Sprintf(`{"stepId":%d,"version":%d}`, waiting.Steps[0].ID, waiting.Version), lin)
	if rejected.Code != http.StatusOK {
		t.Fatalf("驳回后继续 = %d %s", rejected.Code, rejected.Body.String())
	}
	if decodeRun(t, rejected).Status != "failed" {
		t.Fatal("驳回后的执行没有失败")
	}
	if imageOf(t, engine, lin, "order-api", "生产") != "order-api:1.9.0" {
		t.Fatal("未通过关卡却改了镜像")
	}

	back := postJSON(engine, "/api/playbook/runs", fmt.Sprintf(`{"playbook":"release.prod.rollback","idempotencyKey":"rollback-1","input":{"ticket_id":%d,"tree_node_id":%d,"release_item_id":%d,"previous_tag":"1.8.3","clusters":[%d]}}`, ticketID, order.ID, itemID, clusterID), lin)
	if back.Code != http.StatusOK {
		t.Fatalf("回滚 = %d %s", back.Code, back.Body.String())
	}
	backRun := decodeRun(t, back)
	if backRun.Status != "paused" {
		t.Fatalf("回滚状态 = %s %+v", backRun.Status, backRun.Steps)
	}
	if imageOf(t, engine, lin, "order-api", "生产") != "order-api:1.8.3" {
		t.Fatal("回滚没有写回旧标签")
	}

	book := model.Playbook{Code: "draft.demo", Name: "草稿", Status: "draft", InputSchema: `{}`}
	if err := db.Create(&book).Error; err != nil {
		t.Fatal(err)
	}
	if rec := postJSON(engine, "/api/playbook/runs", fmt.Sprintf(`{"playbook":"draft.demo","idempotencyKey":"draft-1","input":{"tree_node_id":%d}}`, order.ID), lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("草稿启动 = %d %s", rec.Code, rec.Body.String())
	}

	fresh := postJSON(engine, "/api/playbook/runs", fmt.Sprintf(`{"playbook":"inspect.host.baseline","idempotencyKey":"inspect-retry","input":{"tree_node_id":%d,"host_ids":%s}}`, order.ID, hostJSON), lin)
	if fresh.Code != http.StatusOK {
		t.Fatalf("待重试巡检 = %d %s", fresh.Code, fresh.Body.String())
	}
	freshRun := decodeRun(t, fresh)
	retried := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/retry", freshRun.ID), "", lin)
	if retried.Code != http.StatusOK {
		t.Fatalf("重试 = %d %s", retried.Code, retried.Body.String())
	}
	newRun := decodeRun(t, retried)
	if newRun.ID == freshRun.ID || newRun.Status == "cancelled" {
		t.Fatalf("新执行 = %+v", newRun)
	}
	var old model.Run
	if err := db.First(&old, freshRun.ID).Error; err != nil {
		t.Fatal(err)
	}
	if old.Status != "cancelled" || !strings.Contains(old.Audit, "由重试取代") {
		t.Fatalf("原执行 = %s %s", old.Status, old.Audit)
	}
	if !strings.HasSuffix(old.IdempotencyKey, "inspect-retry") || !strings.Contains(newRunKey(t, db, newRun.ID), "inspect-retry:") {
		t.Fatal("重试没有换新的幂等键")
	}

	ignored := model.Playbook{Code: "custom.ignore", Name: "忽略失败", Status: "published", InputSchema: `{}`}
	if err := db.Create(&ignored).Error; err != nil {
		t.Fatal(err)
	}
	steps := []model.PlaybookStep{
		{PlaybookID: ignored.ID, Seq: 1, StepKey: "ping", Kind: "http_call", OnError: "ignore", InputMapping: `{"url":"http://127.0.0.1:1"}`},
		{PlaybookID: ignored.ID, Seq: 2, StepKey: "hold", Kind: "wait_manual", OnError: "stop", InputMapping: `{}`},
	}
	if err := db.Create(&steps).Error; err != nil {
		t.Fatal(err)
	}
	skipped := postJSON(engine, "/api/playbook/runs", fmt.Sprintf(`{"playbook":"custom.ignore","idempotencyKey":"ignore-1","input":{"tree_node_id":%d}}`, order.ID), lin)
	if skipped.Code != http.StatusOK {
		t.Fatalf("忽略失败 = %d %s", skipped.Code, skipped.Body.String())
	}
	skipRun := decodeRun(t, skipped)
	if skipRun.Status != "paused" || skipRun.Steps[0].Status != "failed" || skipRun.Steps[1].Status != "waiting" {
		t.Fatalf("忽略后的步骤 = %s %+v", skipRun.Status, skipRun.Steps)
	}
}

func newRunKey(t *testing.T, db *gorm.DB, id uint) string {
	t.Helper()
	var row model.Run
	if err := db.First(&row, id).Error; err != nil {
		t.Fatal(err)
	}
	return row.IdempotencyKey
}

func TestProductionImageOnlyFromPlaybook(t *testing.T) {
	engine, db := playEngine(t)
	lin := loginName(t, engine, "林夏", "secret")
	chen := loginName(t, engine, "陈舟", "secret")
	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	before := imageOf(t, engine, lin, "order-api", "生产")
	prod := mustInstance(t, decodeJSON[[]instanceJSON](t, getAuth(engine, "/api/k8s/instances", lin)), "order-api", "生产")

	tickets := decodeJSON[[]struct {
		ID     uint   `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
	}](t, getAuth(engine, "/api/ticket/instances", lin))
	var changeID uint
	for _, row := range tickets {
		if row.Title == "订单库升配" {
			changeID = row.ID
		}
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", changeID), "", lin); rec.Code != http.StatusOK {
		t.Fatalf("审批变更单 = %d %s", rec.Code, rec.Body.String())
	}
	body := fmt.Sprintf(`{"image":"order-api:9.9.9","replicas":9,"ticketId":%d}`, changeID)
	if rec := putJSON(engine, fmt.Sprintf("/api/k8s/instances/%d", prod.ID), body, lin); rec.Code != http.StatusConflict {
		t.Fatalf("生产实例写入 = %d %s", rec.Code, rec.Body.String())
	}
	if imageOf(t, engine, lin, "order-api", "生产") != before {
		t.Fatal("实例接口改了生产镜像")
	}
	kept := mustInstance(t, decodeJSON[[]instanceJSON](t, getAuth(engine, "/api/k8s/instances", lin)), "order-api", "生产")
	if kept.Image != prod.Image {
		t.Fatal("生产镜像变了")
	}

	orders := decodeJSON[[]orderJSON](t, getAuth(engine, "/api/cicd/orders", lin))
	waiting := mustOrder(t, orders, "order-api", "1.8.4")
	if rec := postJSON(engine, fmt.Sprintf("/api/cicd/orders/%d/confirm", waiting.ID), "", lin); rec.Code != http.StatusConflict {
		t.Fatalf("确认生产阶段 = %d %s", rec.Code, rec.Body.String())
	}
	if imageOf(t, engine, lin, "order-api", "生产") != before {
		t.Fatal("确认生产阶段改了镜像")
	}

	items := decodeJSON[[]namedID](t, getAuth(engine, "/api/cicd/items", lin))
	var itemID uint
	for _, item := range items {
		if item.Name == "order-api" {
			itemID = item.ID
		}
	}
	var clusterID uint
	for _, cluster := range decodeJSON[[]struct {
		ID  uint   `json:"id"`
		Env string `json:"env"`
	}](t, getAuth(engine, "/api/k8s/clusters", lin)) {
		if cluster.Env == "生产" {
			clusterID = cluster.ID
		}
	}
	var releaseTpl uint
	for _, tpl := range decodeJSON[[]namedID](t, getAuth(engine, "/api/ticket/templates", chen)) {
		if tpl.Name == "生产发布" {
			releaseTpl = tpl.ID
		}
	}
	payload := fmt.Sprintf(`{"release_item_id":%d,"image_tag":"1.9.2","clusters":[%d]}`, itemID, clusterID)
	created := postJSON(engine, "/api/ticket/instances", fmt.Sprintf(`{"templateId":%d,"treeNodeId":%d,"title":"只走剧本","payload":%q}`, releaseTpl, order.ID, payload), chen)
	if created.Code != http.StatusOK {
		t.Fatalf("提单 = %d %s", created.Code, created.Body.String())
	}
	ticketID := decodeJSON[struct {
		ID uint `json:"id"`
	}](t, created).ID
	approved := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", ticketID), "", lin)
	if approved.Code != http.StatusOK {
		t.Fatalf("审批生产发布 = %d %s", approved.Code, approved.Body.String())
	}
	runID := decodeJSON[struct {
		RunID uint `json:"runId"`
	}](t, approved).RunID
	if imageOf(t, engine, lin, "order-api", "生产") != "order-api:1.9.2" {
		t.Fatal("剧本部署没有改镜像")
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/cicd/orders/%d/confirm", waiting.ID), "", lin); rec.Code != http.StatusConflict {
		t.Fatalf("确认不能收工单 = %d %s", rec.Code, rec.Body.String())
	}
	var ticketRow model.TicketInstance
	if err := db.First(&ticketRow, ticketID).Error; err != nil {
		t.Fatal(err)
	}
	if ticketRow.Status != "pending_action" {
		t.Fatalf("确认接口把工单收成 %s", ticketRow.Status)
	}
	view := decodeRun(t, getAuth(engine, fmt.Sprintf("/api/playbook/runs/%d", runID), lin))
	var stepID uint
	for _, step := range view.Steps {
		if step.Key == "confirm" {
			stepID = step.ID
		}
	}
	continued := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/continue", runID), fmt.Sprintf(`{"stepId":%d,"version":%d,"continueInput":{}}`, stepID, view.Version), lin)
	if continued.Code != http.StatusOK {
		t.Fatalf("继续 = %d %s", continued.Code, continued.Body.String())
	}
	if decodeRun(t, continued).Status != "success" {
		t.Fatal("没有校验地址时执行没有完成")
	}
	if err := db.First(&ticketRow, ticketID).Error; err != nil {
		t.Fatal(err)
	}
	if ticketRow.Status != "finished" {
		t.Fatalf("继续后工单 = %s", ticketRow.Status)
	}
}

func TestInspectTaskMock(t *testing.T) {
	t.Setenv("XUNTAI_TASK_MOCK", "")
	engine, db := playEngine(t)
	lin := loginName(t, engine, "林夏", "secret")
	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	before := imageOf(t, engine, lin, "order-api", "生产")
	machines := decodeJSON[[]struct {
		ID uint   `json:"id"`
		IP string `json:"ip"`
	}](t, getAuth(engine, fmt.Sprintf("/api/tree/machines?nodeId=%d", order.ID), lin))
	hostIDs := make([]uint, 0, len(machines))
	for _, machine := range machines {
		hostIDs = append(hostIDs, machine.ID)
	}
	hostJSON, _ := json.Marshal(hostIDs)
	body := func(key, extra string) string {
		return fmt.Sprintf(`{"playbook":"inspect.host.baseline","idempotencyKey":"%s","input":{"tree_node_id":%d,"host_ids":%s%s}}`, key, order.ID, hostJSON, extra)
	}
	stuck := postJSON(engine, "/api/playbook/runs", body("inspect-mock-off", `,"task_mock":"failed"`), lin)
	if stuck.Code != http.StatusOK {
		t.Fatalf("关闭模拟 = %d %s", stuck.Code, stuck.Body.String())
	}
	waiting := decodeRun(t, stuck)
	if waiting.Status != "running" {
		t.Fatalf("没开模拟仍结束 = %s", waiting.Status)
	}
	taskID := uint(0)
	for _, step := range waiting.Steps {
		if step.Key == "run_inspect" {
			taskID = uint(step.Output["task_id"].(float64))
		}
	}
	var issued int64
	if err := db.Model(&model.JobResult{}).Where("job_id = ? AND status = ?", taskID, "issued").Count(&issued).Error; err != nil {
		t.Fatal(err)
	}
	if issued == 0 {
		t.Fatal("没有停在已下发")
	}

	t.Setenv("XUNTAI_TASK_MOCK", "1")
	if !playbook.TaskMock() {
		t.Fatal("模拟开关没有打开")
	}
	synced := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/sync", waiting.ID), "", lin)
	if synced.Code != http.StatusOK {
		t.Fatalf("同步模拟 = %d %s", synced.Code, synced.Body.String())
	}
	if decodeRun(t, synced).Status != "failed" {
		t.Fatal("失败回写没有让执行失败")
	}
	var failed int64
	if err := db.Model(&model.JobResult{}).Where("job_id = ? AND status = ?", taskID, "failed").Count(&failed).Error; err != nil {
		t.Fatal(err)
	}
	if failed != int64(len(machines)) {
		t.Fatalf("失败结果 = %d", failed)
	}

	passed := postJSON(engine, "/api/playbook/runs", body("inspect-mock-ok", ""), lin)
	if passed.Code != http.StatusOK {
		t.Fatalf("成功模拟 = %d %s", passed.Code, passed.Body.String())
	}
	okRun := decodeRun(t, passed)
	if okRun.Status != "success" {
		t.Fatalf("成功模拟状态 = %s %+v", okRun.Status, okRun.Steps)
	}
	if imageOf(t, engine, lin, "order-api", "生产") != before {
		t.Fatal("巡检模拟改了生产镜像")
	}
}

func TestReleaseBatchWaits(t *testing.T) {
	engine, _ := playEngine(t)
	lin := loginName(t, engine, "林夏", "secret")
	chen := loginName(t, engine, "陈舟", "secret")
	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	items := decodeJSON[[]namedID](t, getAuth(engine, "/api/cicd/items", lin))
	var itemID uint
	for _, item := range items {
		if item.Name == "order-api" {
			itemID = item.ID
		}
	}
	clusters := decodeJSON[[]struct {
		ID  uint   `json:"id"`
		Env string `json:"env"`
	}](t, getAuth(engine, "/api/k8s/clusters", lin))
	var clusterID uint
	for _, cluster := range clusters {
		if cluster.Env == "生产" {
			clusterID = cluster.ID
		}
	}
	templates := decodeJSON[[]namedID](t, getAuth(engine, "/api/ticket/templates", chen))
	var releaseTpl, rollbackTpl uint
	for _, tpl := range templates {
		if tpl.Name == "生产发布" {
			releaseTpl = tpl.ID
		}
		if tpl.Name == "生产回滚" {
			rollbackTpl = tpl.ID
		}
	}
	if rec := putJSON(engine, fmt.Sprintf("/api/cicd/items/%d", itemID), `{"batches":["batch1","batch2"],"strategy":"canary","verifyUrl":""}`, lin); rec.Code != http.StatusOK {
		t.Fatalf("写入批次 = %d %s", rec.Code, rec.Body.String())
	}
	payload := fmt.Sprintf(`{"release_item_id":%d,"image_tag":"1.9.4","clusters":[%d],"verify_url":"http://127.0.0.1:1","batches":["skip"]}`, itemID, clusterID)
	created := postJSON(engine, "/api/ticket/instances", fmt.Sprintf(`{"templateId":%d,"treeNodeId":%d,"title":"分批发布","payload":%q}`, releaseTpl, order.ID, payload), chen)
	if created.Code != http.StatusOK {
		t.Fatalf("提单 = %d %s", created.Code, created.Body.String())
	}
	ticketID := decodeJSON[struct {
		ID uint `json:"id"`
	}](t, created).ID
	approved := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", ticketID), "", lin)
	if approved.Code != http.StatusOK {
		t.Fatalf("审批 = %d %s", approved.Code, approved.Body.String())
	}
	runID := decodeJSON[struct {
		RunID uint `json:"runId"`
	}](t, approved).RunID
	view := decodeRun(t, getAuth(engine, fmt.Sprintf("/api/playbook/runs/%d", runID), lin))
	if view.Status != "paused" {
		t.Fatalf("第一批 = %s %+v", view.Status, view.Steps)
	}
	if imageOf(t, engine, lin, "order-api", "生产") != "order-api:1.9.4" {
		t.Fatal("第一批等待前没有写镜像")
	}
	var batch1 uint
	for _, step := range view.Steps {
		if step.Key == "verify" || step.Key == "confirm" || step.Key == "skip" {
			t.Fatalf("策略不该来自入参 %+v", view.Steps)
		}
		if step.Key == "deploy" && step.Output["executor"] != "platform" {
			t.Fatalf("执行器 = %+v", step.Output)
		}
		if step.Key == "batch1" && step.Status == "waiting" {
			batch1 = step.ID
		}
	}
	if batch1 == 0 {
		t.Fatalf("没有第一批 %+v", view.Steps)
	}
	dup := postJSON(engine, "/api/playbook/runs", fmt.Sprintf(`{"playbook":"release.prod.single","idempotencyKey":"%d","input":{"ticket_id":%d,"tree_node_id":%d,"release_item_id":%d,"image_tag":"1.9.4","clusters":[%d]}}`, ticketID, ticketID, order.ID, itemID, clusterID), lin)
	if dup.Code != http.StatusConflict {
		t.Fatalf("未结束再次发布 = %d %s", dup.Code, dup.Body.String())
	}
	next := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/continue", view.ID), fmt.Sprintf(`{"stepId":%d,"version":%d}`, batch1, view.Version), lin)
	if next.Code != http.StatusOK {
		t.Fatalf("继续第一批 = %d %s", next.Code, next.Body.String())
	}
	held := decodeRun(t, next)
	if held.Status != "paused" {
		t.Fatalf("第二批 = %s %+v", held.Status, held.Steps)
	}
	var batch2 uint
	for _, step := range held.Steps {
		if step.Key == "batch1" && step.Status != "success" {
			t.Fatalf("第一批没有完成 %+v", held.Steps)
		}
		if step.Key == "batch2" && step.Status == "waiting" {
			batch2 = step.ID
		}
	}
	if batch2 == 0 {
		t.Fatalf("没有第二批 %+v", held.Steps)
	}
	done := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/continue", held.ID), fmt.Sprintf(`{"stepId":%d,"version":%d}`, batch2, held.Version), lin)
	if done.Code != http.StatusOK {
		t.Fatalf("继续第二批 = %d %s", done.Code, done.Body.String())
	}
	if decodeRun(t, done).Status != "success" {
		t.Fatalf("分批结束 = %+v", decodeRun(t, done))
	}

	backPayload := fmt.Sprintf(`{"release_item_id":%d,"previous_tag":"1.8.3","clusters":[%d]}`, itemID, clusterID)
	backTicket := postJSON(engine, "/api/ticket/instances", fmt.Sprintf(`{"templateId":%d,"treeNodeId":%d,"title":"分批回滚","payload":%q}`, rollbackTpl, order.ID, backPayload), chen)
	if backTicket.Code != http.StatusOK {
		t.Fatalf("回滚单 = %d %s", backTicket.Code, backTicket.Body.String())
	}
	backID := decodeJSON[struct {
		ID uint `json:"id"`
	}](t, backTicket).ID
	backApproved := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", backID), "", lin)
	if backApproved.Code != http.StatusOK {
		t.Fatalf("审批回滚 = %d %s", backApproved.Code, backApproved.Body.String())
	}
	backRunID := decodeJSON[struct {
		RunID uint `json:"runId"`
	}](t, backApproved).RunID
	backView := decodeRun(t, getAuth(engine, fmt.Sprintf("/api/playbook/runs/%d", backRunID), lin))
	if backView.Status != "paused" || imageOf(t, engine, lin, "order-api", "生产") != "order-api:1.8.3" {
		t.Fatalf("回滚 = %s %s %+v", backView.Status, imageOf(t, engine, lin, "order-api", "生产"), backView.Steps)
	}
	sawBatch := false
	for _, step := range backView.Steps {
		if step.Key == "batch1" && step.Status == "waiting" {
			sawBatch = true
		}
	}
	if !sawBatch {
		t.Fatalf("回滚没有走批次 %+v", backView.Steps)
	}
}

func TestRolloutCanaryMock(t *testing.T) {
	t.Setenv("XUNTAI_ROLLOUTS_MOCK", "1")
	engine, _ := playEngine(t)
	lin := loginName(t, engine, "林夏", "secret")
	chen := loginName(t, engine, "陈舟", "secret")
	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	itemID, clusterID := releaseIDs(t, engine, lin)
	releaseTpl, rollbackTpl := releaseTemplates(t, engine, chen)
	if rec := putJSON(engine, fmt.Sprintf("/api/cicd/items/%d", itemID), `{"executor":"rollouts","strategy":"canary","weights":[10,50],"stableSeconds":5}`, lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("百分比未到 100 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := putJSON(engine, fmt.Sprintf("/api/cicd/items/%d", itemID), `{"executor":"rollouts","strategy":"canary","weights":[10,50,100],"stableSeconds":5,"failureThreshold":1,"autoRollback":false}`, lin); rec.Code != http.StatusOK {
		t.Fatalf("写入灰度 = %d %s", rec.Code, rec.Body.String())
	}
	before := imageOf(t, engine, lin, "order-api", "生产")
	payload := fmt.Sprintf(`{"release_item_id":%d,"image_tag":"1.9.5","clusters":[%d],"executor":"platform","weights":[1],"strategy":"manual"}`, itemID, clusterID)
	run := approveRelease(t, engine, chen, lin, releaseTpl, order.ID, "灰度发布", payload)
	if run.Status != "paused" || imageOf(t, engine, lin, "order-api", "生产") != before {
		t.Fatalf("第一阶段 = %s %s %+v", run.Status, imageOf(t, engine, lin, "order-api", "生产"), run.Steps)
	}
	if !sawStep(run, "w10", "waiting") || sawStep(run, "w1", "") || sawStep(run, "confirm", "") {
		t.Fatalf("步骤没按发布项展开 %+v", run.Steps)
	}
	if stepOut(run, "w10")["mode"] != "mock" || stepOut(run, "w10")["weight"] != float64(10) || stepOut(run, "w10")["from"] != float64(0) || stepOut(run, "w10")["stable_seconds"] != float64(5) {
		t.Fatalf("第一阶段输出 = %+v", stepOut(run, "w10"))
	}
	dup := postJSON(engine, "/api/playbook/runs", fmt.Sprintf(`{"playbook":"release.prod.single","idempotencyKey":"%s","input":{"ticket_id":1,"tree_node_id":%d,"release_item_id":%d,"image_tag":"1.9.5","clusters":[%d]}}`, runKey(t, engine, lin, run.ID), order.ID, itemID, clusterID), lin)
	if dup.Code != http.StatusConflict {
		t.Fatalf("未结束再次发布 = %d %s", dup.Code, dup.Body.String())
	}
	run = continueWaiting(t, engine, lin, run)
	if run.Status != "paused" || !sawStep(run, "w50", "waiting") || imageOf(t, engine, lin, "order-api", "生产") != before {
		t.Fatalf("第二阶段 = %s %+v", run.Status, run.Steps)
	}
	run = continueWaiting(t, engine, lin, run)
	if run.Status != "paused" || !sawStep(run, "w100", "waiting") || imageOf(t, engine, lin, "order-api", "生产") != "order-api:1.9.5" {
		t.Fatalf("全量 = %s %s %+v", run.Status, imageOf(t, engine, lin, "order-api", "生产"), run.Steps)
	}
	run = continueWaiting(t, engine, lin, run)
	if run.Status != "success" {
		t.Fatalf("灰度结束 = %s %+v", run.Status, run.Steps)
	}

	back := approveRelease(t, engine, chen, lin, rollbackTpl, order.ID, "灰度回滚", fmt.Sprintf(`{"release_item_id":%d,"previous_tag":"1.8.3","clusters":[%d],"executor":"platform"}`, itemID, clusterID))
	if back.Status != "paused" || !sawStep(back, "abort", "waiting") || imageOf(t, engine, lin, "order-api", "生产") != "order-api:1.8.3" {
		t.Fatalf("回滚 = %s %s %+v", back.Status, imageOf(t, engine, lin, "order-api", "生产"), back.Steps)
	}
}

func TestRolloutAutoRollbackAndLivePath(t *testing.T) {
	t.Setenv("XUNTAI_ROLLOUTS_MOCK", "1")
	engine, _ := playEngine(t)
	lin := loginName(t, engine, "林夏", "secret")
	chen := loginName(t, engine, "陈舟", "secret")
	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	itemID, clusterID := releaseIDs(t, engine, lin)
	releaseTpl, _ := releaseTemplates(t, engine, chen)
	if rec := putJSON(engine, fmt.Sprintf("/api/cicd/items/%d", itemID), `{"executor":"rollouts","strategy":"canary","weights":[10,100],"autoRollback":true}`, lin); rec.Code != http.StatusOK {
		t.Fatalf("自动回滚策略 = %d %s", rec.Code, rec.Body.String())
	}
	before := imageOf(t, engine, lin, "order-api", "生产")
	run := approveRelease(t, engine, chen, lin, releaseTpl, order.ID, "失败回滚", fmt.Sprintf(`{"release_item_id":%d,"image_tag":"1.9.6","clusters":[%d]}`, itemID, clusterID))
	run = continueWaiting(t, engine, lin, run)
	if imageOf(t, engine, lin, "order-api", "生产") != "order-api:1.9.6" || !sawStep(run, "w100", "waiting") {
		t.Fatalf("全量等待 = %s %s %+v", run.Status, imageOf(t, engine, lin, "order-api", "生产"), run.Steps)
	}
	failed := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/continue", run.ID), fmt.Sprintf(`{"stepId":%d,"version":%d,"continueInput":{"failed":true}}`, waitingID(run), run.Version), lin)
	if failed.Code != http.StatusOK {
		t.Fatalf("失败继续 = %d %s", failed.Code, failed.Body.String())
	}
	done := decodeRun(t, failed)
	if done.Status != "failed" || imageOf(t, engine, lin, "order-api", "生产") != before {
		t.Fatalf("自动回滚 = %s %s %+v", done.Status, imageOf(t, engine, lin, "order-api", "生产"), done.Steps)
	}

	t.Setenv("XUNTAI_ROLLOUTS_MOCK", "")
	liveEngine, _ := playEngine(t)
	lin = loginName(t, liveEngine, "林夏", "secret")
	chen = loginName(t, liveEngine, "陈舟", "secret")
	nodes = decodeNodes(t, getAuth(liveEngine, "/api/tree/nodes", lin))
	order = mustNode(t, nodes, "订单")
	itemID, clusterID = releaseIDs(t, liveEngine, lin)
	releaseTpl, _ = releaseTemplates(t, liveEngine, chen)
	if rec := putJSON(liveEngine, fmt.Sprintf("/api/cicd/items/%d", itemID), `{"executor":"rollouts","weights":[10,100]}`, lin); rec.Code != http.StatusOK {
		t.Fatalf("真实路径策略 = %d %s", rec.Code, rec.Body.String())
	}
	origin := imageOf(t, liveEngine, lin, "order-api", "生产")
	live := approveRelease(t, liveEngine, chen, lin, releaseTpl, order.ID, "未模拟", fmt.Sprintf(`{"release_item_id":%d,"image_tag":"1.9.7","clusters":[%d]}`, itemID, clusterID))
	if live.Status != "failed" || imageOf(t, liveEngine, lin, "order-api", "生产") != origin {
		t.Fatalf("未模拟 = %s %s %+v", live.Status, imageOf(t, liveEngine, lin, "order-api", "生产"), live.Steps)
	}
	if !strings.Contains(stepErr(live, "w10"), "kubeconfig") {
		t.Fatalf("真实路径错误 = %+v", live.Steps)
	}
}

func releaseIDs(t *testing.T, engine http.Handler, token string) (uint, uint) {
	t.Helper()
	items := decodeJSON[[]namedID](t, getAuth(engine, "/api/cicd/items", token))
	var itemID uint
	for _, item := range items {
		if item.Name == "order-api" {
			itemID = item.ID
		}
	}
	clusters := decodeJSON[[]struct {
		ID  uint   `json:"id"`
		Env string `json:"env"`
	}](t, getAuth(engine, "/api/k8s/clusters", token))
	var clusterID uint
	for _, cluster := range clusters {
		if cluster.Env == "生产" {
			clusterID = cluster.ID
		}
	}
	return itemID, clusterID
}

func releaseTemplates(t *testing.T, engine http.Handler, token string) (uint, uint) {
	t.Helper()
	templates := decodeJSON[[]namedID](t, getAuth(engine, "/api/ticket/templates", token))
	var releaseTpl, rollbackTpl uint
	for _, tpl := range templates {
		if tpl.Name == "生产发布" {
			releaseTpl = tpl.ID
		}
		if tpl.Name == "生产回滚" {
			rollbackTpl = tpl.ID
		}
	}
	return releaseTpl, rollbackTpl
}

func approveRelease(t *testing.T, engine http.Handler, chen, lin string, tpl, nodeID uint, title, payload string) runView {
	t.Helper()
	created := postJSON(engine, "/api/ticket/instances", fmt.Sprintf(`{"templateId":%d,"treeNodeId":%d,"title":%q,"payload":%q}`, tpl, nodeID, title, payload), chen)
	if created.Code != http.StatusOK {
		t.Fatalf("提单 = %d %s", created.Code, created.Body.String())
	}
	ticketID := decodeJSON[struct {
		ID uint `json:"id"`
	}](t, created).ID
	approved := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", ticketID), "", lin)
	if approved.Code != http.StatusOK {
		t.Fatalf("审批 = %d %s", approved.Code, approved.Body.String())
	}
	runID := decodeJSON[struct {
		RunID uint `json:"runId"`
	}](t, approved).RunID
	return decodeRun(t, getAuth(engine, fmt.Sprintf("/api/playbook/runs/%d", runID), lin))
}

func continueWaiting(t *testing.T, engine http.Handler, token string, run runView) runView {
	t.Helper()
	rec := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/continue", run.ID), fmt.Sprintf(`{"stepId":%d,"version":%d}`, waitingID(run), run.Version), token)
	if rec.Code != http.StatusOK {
		t.Fatalf("继续 = %d %s", rec.Code, rec.Body.String())
	}
	return decodeRun(t, rec)
}

func waitingID(run runView) uint {
	for _, step := range run.Steps {
		if step.Status == "waiting" {
			return step.ID
		}
	}
	return 0
}

func sawStep(run runView, key, status string) bool {
	for _, step := range run.Steps {
		if step.Key == key && (status == "" || step.Status == status) {
			return true
		}
	}
	return false
}

func stepOut(run runView, key string) map[string]any {
	for _, step := range run.Steps {
		if step.Key == key {
			return step.Output
		}
	}
	return nil
}

func stepErr(run runView, key string) string {
	for _, step := range run.Steps {
		if step.Key == key {
			return step.Error
		}
	}
	return ""
}

func runKey(t *testing.T, engine http.Handler, token string, id uint) string {
	t.Helper()
	view := decodeJSON[struct {
		Key string `json:"idempotencyKey"`
	}](t, getAuth(engine, fmt.Sprintf("/api/playbook/runs/%d", id), token))
	return view.Key
}

func playEngine(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Seed(db, "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Seed(db, "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := ticket.Seed(db); err != nil {
		t.Fatal(err)
	}
	if _, err := task.Seed(db); err != nil {
		t.Fatal(err)
	}
	if _, err := k8s.Seed(db); err != nil {
		t.Fatal(err)
	}
	if _, err := cicd.Seed(db); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdb.Seed(db); err != nil {
		t.Fatal(err)
	}
	if _, err := playbook.Seed(db); err != nil {
		t.Fatal(err)
	}
	gate := access.New()
	if err := gate.Reload(db); err != nil {
		t.Fatal(err)
	}
	engine := New(Deps{
		Base:     apibase.Deps{DB: db, Secret: "test-secret", Gate: gate},
		Tree:     apitree.Deps{DB: db},
		Ticket:   apiticket.Deps{DB: db},
		Task:     apitask.Deps{DB: db},
		K8s:      apik8s.Deps{DB: db},
		Cicd:     apicicd.Deps{DB: db},
		Cmdb:     apicmdb.Deps{DB: db},
		Playbook: apiplay.Deps{DB: db},
	})
	return engine, db
}

type runView struct {
	ID      uint   `json:"id"`
	Status  string `json:"status"`
	Version int    `json:"version"`
	Steps   []struct {
		ID     uint           `json:"id"`
		Key    string         `json:"key"`
		Status string         `json:"status"`
		Output map[string]any `json:"output"`
		Error  string         `json:"error"`
	} `json:"steps"`
}

func decodeRun(t *testing.T, rec *httptest.ResponseRecorder) runView {
	t.Helper()
	return decodeJSON[runView](t, rec)
}

func deleteJSON(engine http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}
