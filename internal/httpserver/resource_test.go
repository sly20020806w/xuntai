package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"xuntai/internal/access"
)

func TestResourceWriteChecks(t *testing.T) {
	t.Setenv("XUNTAI_TASK_MOCK", "")
	engine, _ := playEngine(t)
	for _, route := range access.Routes {
		if !access.KnownVerb(route.Verb) || !access.KnownResource(route.Resource) {
			t.Fatalf("写接口动词或资源类型不在枚举里 %+v", route)
		}
	}
	seen := map[string]string{}
	for _, route := range access.Routes {
		seen[route.Method+" "+route.Path] = route.Verb
	}
	for _, path := range []string{
		"POST /api/ticket/instances/:id/approve",
		"POST /api/ticket/instances/:id/reject",
		"POST /api/playbook/runs",
		"POST /api/playbook/runs/:id/continue",
		"POST /api/playbook/runs/:id/cancel",
		"PUT /api/k8s/instances/:id",
		"POST /api/cmdb/models",
		"POST /api/cicd/orders/:id/confirm",
		"POST /api/task/jobs",
	} {
		if seen[path] == "" {
			t.Fatalf("写接口没有接入 %s", path)
		}
	}
	if seen["POST /api/ticket/instances/:id/approve"] != access.VerbApprove {
		t.Fatal("审批动词不对")
	}

	lin := loginName(t, engine, "林夏", "secret")
	zhou := loginName(t, engine, "周宁", "secret")
	xu := loginName(t, engine, "许衡", "secret")
	chen := loginName(t, engine, "陈舟", "secret")
	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	instances := decodeJSON[[]instanceJSON](t, getAuth(engine, "/api/k8s/instances", lin))
	prod := mustInstance(t, instances, "order-api", "生产")
	dev := mustInstance(t, instances, "order-api", "开发")
	before := prod.Image

	devBody := `{"image":"order-api:1.8.3-dev-ops","replicas":1}`
	if rec := putJSON(engine, fmt.Sprintf("/api/k8s/instances/%d", dev.ID), devBody, chen); rec.Code != http.StatusForbidden {
		t.Fatalf("陈舟改开发实例 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := putJSON(engine, fmt.Sprintf("/api/k8s/instances/%d", dev.ID), devBody, xu); rec.Code != http.StatusForbidden {
		t.Fatalf("许衡改开发实例 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := putJSON(engine, fmt.Sprintf("/api/k8s/instances/%d", dev.ID), devBody, lin); rec.Code != http.StatusOK {
		t.Fatalf("林夏改开发实例 = %d %s", rec.Code, rec.Body.String())
	}
	prodBody := `{"image":"order-api:should-not","replicas":9}`
	if rec := putJSON(engine, fmt.Sprintf("/api/k8s/instances/%d", prod.ID), prodBody, zhou); rec.Code != http.StatusConflict {
		t.Fatalf("周宁改生产实例 = %d %s", rec.Code, rec.Body.String())
	}
	if imageOf(t, engine, lin, "order-api", "生产") != before {
		t.Fatal("平台管理员把生产镜像写掉了")
	}

	templates := decodeJSON[[]namedID](t, getAuth(engine, "/api/ticket/templates", chen))
	var changeID uint
	for _, tpl := range templates {
		if tpl.Name == "变更申请" {
			changeID = tpl.ID
		}
	}
	created := postJSON(engine, "/api/ticket/instances", fmt.Sprintf(`{"templateId":%d,"treeNodeId":%d,"title":"资源归属"}`, changeID, order.ID), chen)
	if created.Code != http.StatusOK {
		t.Fatalf("提单 = %d %s", created.Code, created.Body.String())
	}
	ticketID := decodeJSON[struct {
		ID uint `json:"id"`
	}](t, created).ID
	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", ticketID), "", chen); rec.Code != http.StatusForbidden {
		t.Fatalf("陈舟审批 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/reject", ticketID), "", xu); rec.Code != http.StatusForbidden {
		t.Fatalf("许衡驳回 = %d %s", rec.Code, rec.Body.String())
	}
	approved := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", ticketID), "", zhou)
	if approved.Code != http.StatusOK {
		t.Fatalf("周宁审批 = %d %s", approved.Code, approved.Body.String())
	}

	machines := decodeJSON[[]struct {
		ID uint `json:"id"`
	}](t, getAuth(engine, fmt.Sprintf("/api/tree/machines?nodeId=%d", order.ID), lin))
	hostIDs := make([]uint, 0, len(machines))
	for _, machine := range machines {
		hostIDs = append(hostIDs, machine.ID)
	}
	hostJSON, _ := json.Marshal(hostIDs)
	start := fmt.Sprintf(`{"playbook":"inspect.host.baseline","idempotencyKey":"resource-inspect","input":{"tree_node_id":%d,"host_ids":%s}}`, order.ID, hostJSON)
	if rec := postJSON(engine, "/api/playbook/runs", start, xu); rec.Code != http.StatusForbidden {
		t.Fatalf("许衡启动 = %d %s", rec.Code, rec.Body.String())
	}
	started := postJSON(engine, "/api/playbook/runs", start, lin)
	if started.Code != http.StatusOK {
		t.Fatalf("林夏启动 = %d %s", started.Code, started.Body.String())
	}
	run := decodeRun(t, started)
	if run.Status != "running" {
		t.Fatalf("巡检 = %s", run.Status)
	}
	if rec := postJSON(engine, "/api/playbook/runs", start, lin); rec.Code != http.StatusConflict {
		t.Fatalf("重复启动 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/cancel", run.ID), "", xu); rec.Code != http.StatusForbidden {
		t.Fatalf("许衡取消 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/cancel", run.ID), "", lin); rec.Code != http.StatusOK {
		t.Fatalf("林夏取消 = %d %s", rec.Code, rec.Body.String())
	}

	if rec := postJSON(engine, "/api/cmdb/models", `{"name":"未挂树","code":"loose"}`, lin); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "资源未挂载到服务树，无法校验归属") {
		t.Fatalf("林夏建模型 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/cmdb/objects", `{"modelId":1,"name":"悬空","treeNodeId":0}`, lin); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "资源未挂载到服务树，无法校验归属") {
		t.Fatalf("未挂树对象 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/cmdb/models", `{"name":"目录","code":"catalog"}`, zhou); rec.Code != http.StatusOK {
		t.Fatalf("周宁建模型 = %d %s", rec.Code, rec.Body.String())
	}

	orders := decodeJSON[[]orderJSON](t, getAuth(engine, "/api/cicd/orders", lin))
	waiting := mustOrder(t, orders, "order-api", "1.8.4")
	confirm := fmt.Sprintf("/api/cicd/orders/%d/confirm", waiting.ID)
	if rec := postJSON(engine, confirm, "", chen); rec.Code != http.StatusForbidden {
		t.Fatalf("陈舟确认 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, confirm, "", lin); rec.Code != http.StatusConflict {
		t.Fatalf("林夏确认生产 = %d %s", rec.Code, rec.Body.String())
	}
	if imageOf(t, engine, lin, "order-api", "生产") != before {
		t.Fatal("确认生产改了镜像")
	}
}
