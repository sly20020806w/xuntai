package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"xuntai/internal/access"
	apibase "xuntai/internal/api/base"
	apicicd "xuntai/internal/api/cicd"
	apik8s "xuntai/internal/api/k8s"
	apimonitor "xuntai/internal/api/monitor"
	apitask "xuntai/internal/api/task"
	apiticket "xuntai/internal/api/ticket"
	apitree "xuntai/internal/api/tree"
	"xuntai/internal/base"
	cicdcore "xuntai/internal/cicd"
	"xuntai/internal/k8s"
	"xuntai/internal/model"
	"xuntai/internal/monitor"
	"xuntai/internal/task"
	"xuntai/internal/ticket"
	"xuntai/internal/tree"
)

func TestReleaseStagesAndRepublish(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.Migrate(db); err != nil {
		t.Fatal(err)
	}
	steps := []func(*gorm.DB) error{
		func(db *gorm.DB) error { _, err := base.Seed(db, "secret"); return err },
		func(db *gorm.DB) error { _, err := tree.Seed(db, "secret"); return err },
		func(db *gorm.DB) error { _, err := ticket.Seed(db); return err },
		func(db *gorm.DB) error { _, err := task.Seed(db); return err },
		func(db *gorm.DB) error { _, err := monitor.Seed(db); return err },
		func(db *gorm.DB) error { _, err := k8s.Seed(db); return err },
		func(db *gorm.DB) error { _, err := cicdcore.Seed(db); return err },
	}
	for _, step := range steps {
		if err := step(db); err != nil {
			t.Fatal(err)
		}
	}
	gate := access.New()
	if err := gate.Reload(db); err != nil {
		t.Fatal(err)
	}
	engine := New(Deps{
		Base:    apibase.Deps{DB: db, Secret: "test-secret", Gate: gate},
		Tree:    apitree.Deps{DB: db},
		Ticket:  apiticket.Deps{DB: db},
		Task:    apitask.Deps{DB: db},
		Monitor: apimonitor.Deps{DB: db},
		K8s:     apik8s.Deps{DB: db},
		Cicd:    apicicd.Deps{DB: db},
	})
	for _, route := range engine.Routes() {
		if route.Path == "/api/cicd/rollback" {
			t.Fatal("不该有撤回发布的接口")
		}
	}

	zhou := loginName(t, engine, "周宁", "secret")
	lin := loginName(t, engine, "林夏", "secret")
	xu := loginName(t, engine, "许衡", "secret")
	chen := loginName(t, engine, "陈舟", "secret")

	orders := decodeJSON[[]orderJSON](t, getAuth(engine, "/api/cicd/orders", lin))
	waiting := mustOrder(t, orders, "order-api", "1.8.4")
	if waiting.Status != "running" || !stagePending(waiting, "生产") {
		t.Fatalf("生产单应该停在生产前 %+v", waiting)
	}
	if _, ok := findOrder(orders, "pay-gateway", "2.2.0-dev"); !ok {
		t.Fatal("林夏看不见支付发布")
	}
	hiddenOrders := decodeJSON[[]orderJSON](t, getAuth(engine, "/api/cicd/orders", xu))
	if _, ok := findOrder(hiddenOrders, "order-api", "1.8.4"); ok {
		t.Fatal("许衡看见了订单发布")
	}
	confirmPath := fmt.Sprintf("/api/cicd/orders/%d/confirm", waiting.ID)
	if rec := postJSON(engine, confirmPath, "", zhou); rec.Code != http.StatusForbidden {
		t.Fatalf("周宁确认 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, confirmPath, "", chen); rec.Code != http.StatusForbidden {
		t.Fatalf("陈舟确认 = %d %s", rec.Code, rec.Body.String())
	}
	beforeProd := imageOf(t, engine, lin, "order-api", "生产")
	if rec := postJSON(engine, confirmPath, "", lin); rec.Code != http.StatusConflict {
		t.Fatalf("林夏确认生产 = %d %s", rec.Code, rec.Body.String())
	}
	if imageOf(t, engine, lin, "order-api", "生产") != beforeProd {
		t.Fatal("确认生产阶段改了生产镜像")
	}

	items := decodeJSON[[]namedID](t, getAuth(engine, "/api/cicd/items", lin))
	item := mustNamed(t, items, "order-api")
	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	orderNode := mustNode(t, nodes, "订单")
	trade := mustNode(t, nodes, "交易")
	if rec := postJSON(engine, "/api/cicd/items", fmt.Sprintf(`{"name":"越权项","treeNodeId":%d}`, orderNode.ID), zhou); rec.Code != http.StatusForbidden {
		t.Fatalf("周宁建发布项 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/cicd/items", fmt.Sprintf(`{"name":"父节点项","treeNodeId":%d}`, trade.ID), lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("父节点发布项 = %d %s", rec.Code, rec.Body.String())
	}

	devBody := fmt.Sprintf(`{"itemId":%d,"tag":"1.8.5-dev","env":"开发"}`, item.ID)
	dev := postJSON(engine, "/api/cicd/orders", devBody, lin)
	if dev.Code != http.StatusOK {
		t.Fatalf("开发发布 = %d %s", dev.Code, dev.Body.String())
	}
	var devOrder struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(dev.Body.Bytes(), &devOrder); err != nil || devOrder.Status != "finished" {
		t.Fatalf("开发单应直接完成 %+v %s", devOrder, dev.Body.String())
	}
	if imageOf(t, engine, lin, "order-api", "开发") != "order-api:1.8.5-dev" {
		t.Fatal("开发镜像没有直接换上")
	}

	if rec := postJSON(engine, "/api/cicd/orders", fmt.Sprintf(`{"itemId":%d,"tag":"1.8.3","env":"生产"}`, item.ID), lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("无工单的生产发布 = %d %s", rec.Code, rec.Body.String())
	}
	templates := decodeJSON[[]namedID](t, getAuth(engine, "/api/ticket/templates", chen))
	tpl := templates[0].ID
	created := postJSON(engine, "/api/ticket/instances", fmt.Sprintf(`{"templateId":%d,"treeNodeId":%d,"title":"订单回滚到旧标签"}`, tpl, orderNode.ID), chen)
	if created.Code != http.StatusOK {
		t.Fatalf("提单 = %d %s", created.Code, created.Body.String())
	}
	var ticketID struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &ticketID); err != nil {
		t.Fatal(err)
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", ticketID.ID), "", lin); rec.Code != http.StatusOK {
		t.Fatalf("审批 = %d %s", rec.Code, rec.Body.String())
	}
	rollback := postJSON(engine, "/api/cicd/orders", fmt.Sprintf(`{"itemId":%d,"tag":"1.8.3","env":"生产","ticketId":%d}`, item.ID, ticketID.ID), lin)
	if rollback.Code != http.StatusOK {
		t.Fatalf("再发旧标签 = %d %s", rollback.Code, rollback.Body.String())
	}
	var again struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(rollback.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		rec := postJSON(engine, fmt.Sprintf("/api/cicd/orders/%d/confirm", again.ID), "", lin)
		if rec.Code != http.StatusOK {
			t.Fatalf("确认第 %d 阶段 = %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/cicd/orders/%d/confirm", again.ID), "", lin); rec.Code != http.StatusConflict {
		t.Fatalf("确认生产阶段 = %d %s", rec.Code, rec.Body.String())
	}
	if imageOf(t, engine, lin, "order-api", "生产") != beforeProd {
		t.Fatal("生产阶段确认改了生产镜像")
	}
}

type orderJSON struct {
	ID       uint   `json:"id"`
	ItemName string `json:"itemName"`
	Tag      string `json:"tag"`
	Env      string `json:"env"`
	Status   string `json:"status"`
	Stages   []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"stages"`
}

func stagePending(order orderJSON, name string) bool {
	for _, stage := range order.Stages {
		if stage.Name == name {
			return stage.Status == "pending"
		}
	}
	return false
}

func mustOrder(t *testing.T, rows []orderJSON, item, tag string) orderJSON {
	t.Helper()
	row, ok := findOrder(rows, item, tag)
	if !ok {
		t.Fatalf("没有发布 %s %s", item, tag)
	}
	return row
}

func findOrder(rows []orderJSON, item, tag string) (orderJSON, bool) {
	for _, row := range rows {
		if row.ItemName == item && row.Tag == tag {
			return row, true
		}
	}
	return orderJSON{}, false
}

func imageOf(t *testing.T, engine http.Handler, token, app, env string) string {
	t.Helper()
	rows := decodeJSON[[]instanceJSON](t, getAuth(engine, "/api/k8s/instances", token))
	return mustInstance(t, rows, app, env).Image
}
