package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"xuntai/internal/access"
	apibase "xuntai/internal/api/base"
	apik8s "xuntai/internal/api/k8s"
	apimonitor "xuntai/internal/api/monitor"
	apitask "xuntai/internal/api/task"
	apiticket "xuntai/internal/api/ticket"
	apitree "xuntai/internal/api/tree"
	"xuntai/internal/base"
	k8score "xuntai/internal/k8s"
	"xuntai/internal/model"
	"xuntai/internal/monitor"
	"xuntai/internal/task"
	"xuntai/internal/ticket"
	"xuntai/internal/tree"
)

func TestClusterRecordAndProdTicket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, seed := range []func(*gorm.DB) error{
		func(db *gorm.DB) error { _, err := base.Seed(db, "secret"); return err },
		func(db *gorm.DB) error { _, err := tree.Seed(db, "secret"); return err },
		func(db *gorm.DB) error { _, err := ticket.Seed(db); return err },
		func(db *gorm.DB) error { _, err := task.Seed(db); return err },
		func(db *gorm.DB) error { _, err := monitor.Seed(db); return err },
		func(db *gorm.DB) error { _, err := k8score.Seed(db); return err },
	} {
		if err := seed(db); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&model.Cluster{}).Where("name = ?", "prod-a").Update("kubeconfig", "secret-kubeconfig").Error; err != nil {
		t.Fatal(err)
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
	})

	zhou := loginName(t, engine, "周宁", "secret")
	lin := loginName(t, engine, "林夏", "secret")
	xu := loginName(t, engine, "许衡", "secret")
	chen := loginName(t, engine, "陈舟", "secret")

	clusters := getAuth(engine, "/api/k8s/clusters", xu)
	if clusters.Code != http.StatusOK || strings.Contains(clusters.Body.String(), "secret-kubeconfig") || strings.Contains(clusters.Body.String(), "kubeconfig") {
		t.Fatalf("集群列表泄露了接入配置 %d %s", clusters.Code, clusters.Body.String())
	}
	var clusterRows []struct {
		ID        uint   `json:"id"`
		Name      string `json:"name"`
		Connected bool   `json:"connected"`
	}
	if err := json.Unmarshal(clusters.Body.Bytes(), &clusterRows); err != nil {
		t.Fatal(err)
	}
	var prodID uint
	for _, row := range clusterRows {
		if row.Name == "prod-a" {
			prodID = row.ID
			if row.Connected {
				t.Fatal("没有连接的集群被标成已连接")
			}
		}
	}
	nodes := decodeJSON[struct {
		Connected bool  `json:"connected"`
		Nodes     []any `json:"nodes"`
	}](t, getAuth(engine, fmt.Sprintf("/api/k8s/clusters/%d/nodes", prodID), zhou))
	if nodes.Connected || len(nodes.Nodes) != 0 {
		t.Fatalf("节点不该落成假数据 %+v", nodes)
	}
	if rec := postJSON(engine, "/api/k8s/clusters", `{"name":"dev-b","env":"开发","version":"1.29"}`, lin); rec.Code != http.StatusForbidden {
		t.Fatalf("林夏登记集群 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/k8s/clusters", `{"name":"dev-b","env":"开发","version":"1.29"}`, zhou); rec.Code != http.StatusOK {
		t.Fatalf("周宁登记集群 = %d %s", rec.Code, rec.Body.String())
	}

	instances := decodeJSON[[]instanceJSON](t, getAuth(engine, "/api/k8s/instances", lin))
	prod := mustInstance(t, instances, "order-api", "生产")
	dev := mustInstance(t, instances, "order-api", "开发")
	if _, ok := findInstance(instances, "pay-gateway", "生产"); !ok {
		t.Fatal("林夏看不见支付实例")
	}
	hidden := decodeJSON[[]instanceJSON](t, getAuth(engine, "/api/k8s/instances", xu))
	if _, ok := findInstance(hidden, "order-api", "生产"); ok {
		t.Fatal("许衡看见了订单实例")
	}

	tickets := decodeJSON[[]struct {
		ID     uint   `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
	}](t, getAuth(engine, "/api/ticket/instances", lin))
	var ticketID uint
	for _, row := range tickets {
		if row.Title == "订单库升配" && row.Status == "pending_approve" {
			ticketID = row.ID
		}
	}
	if ticketID == 0 {
		t.Fatal("没有待审批的订单工单")
	}
	body := fmt.Sprintf(`{"image":"order-api:1.8.4","replicas":6,"ticketId":%d}`, ticketID)
	if rec := putJSON(engine, fmt.Sprintf("/api/k8s/instances/%d", prod.ID), body, zhou); rec.Code != http.StatusForbidden {
		t.Fatalf("周宁改生产镜像 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := putJSON(engine, fmt.Sprintf("/api/k8s/instances/%d", prod.ID), body, chen); rec.Code != http.StatusForbidden {
		t.Fatalf("陈舟改生产镜像 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := putJSON(engine, fmt.Sprintf("/api/k8s/instances/%d", prod.ID), body, lin); rec.Code != http.StatusConflict {
		t.Fatalf("未审批就改生产 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", ticketID), "", lin); rec.Code != http.StatusOK {
		t.Fatalf("林夏审批 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := putJSON(engine, fmt.Sprintf("/api/k8s/instances/%d", prod.ID), body, lin); rec.Code != http.StatusConflict {
		t.Fatalf("审批后改生产 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := putJSON(engine, fmt.Sprintf("/api/k8s/instances/%d", dev.ID), `{"image":"order-api:1.8.4-dev","replicas":1}`, lin); rec.Code != http.StatusOK {
		t.Fatalf("开发环境改镜像 = %d %s", rec.Code, rec.Body.String())
	}
	again := decodeJSON[[]instanceJSON](t, getAuth(engine, "/api/k8s/instances", lin))
	if mustInstance(t, again, "order-api", "生产").Image != prod.Image {
		t.Fatal("生产镜像被实例接口改掉了")
	}
}

type instanceJSON struct {
	ID      uint   `json:"id"`
	AppName string `json:"appName"`
	Env     string `json:"env"`
	Image   string `json:"image"`
}

func mustInstance(t *testing.T, rows []instanceJSON, app, env string) instanceJSON {
	t.Helper()
	row, ok := findInstance(rows, app, env)
	if !ok {
		t.Fatalf("没有实例 %s %s", app, env)
	}
	return row
}

func findInstance(rows []instanceJSON, app, env string) (instanceJSON, bool) {
	for _, row := range rows {
		if row.AppName == app && row.Env == env {
			return row, true
		}
	}
	return instanceJSON{}, false
}
