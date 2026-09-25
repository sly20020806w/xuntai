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
	xu := loginName(t, engine, "许衡", "secret")
	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	before := decodeJSON[[]struct {
		ID uint `json:"id"`
	}](t, getAuth(engine, "/api/tree/machines?nodeId="+fmt.Sprint(order.ID), lin))
	if len(before) < 2 {
		t.Fatalf("订单机器 = %d", len(before))
	}

	created := postJSON(engine, "/api/cmdb/models", `{"name":"配置项","code":"ci","remark":"静态"}`, lin)
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
	if rec := deleteJSON(engine, fmt.Sprintf("/api/cmdb/models/%d", modelID), lin); rec.Code != http.StatusOK {
		t.Fatalf("删模型 = %d %s", rec.Code, rec.Body.String())
	}
	_ = db
}

func TestSerialPlaybookTicketAndIdempotency(t *testing.T) {
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
	payload := fmt.Sprintf(`{"release_item_id":%d,"image_tag":"1.9.0","clusters":[%d],"verify_url":"%s"}`, itemID, clusterID, verify.URL)
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
