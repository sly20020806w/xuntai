package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"xuntai/internal/access"
	apibase "xuntai/internal/api/base"
	apiticket "xuntai/internal/api/ticket"
	apitree "xuntai/internal/api/tree"
	"xuntai/internal/base"
	"xuntai/internal/model"
	"xuntai/internal/ticket"
	"xuntai/internal/tree"
)

func TestTicketApprovalBeforeAction(t *testing.T) {
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
	gate := access.New()
	if err := gate.Reload(db); err != nil {
		t.Fatal(err)
	}
	engine := New(Deps{
		Base:   apibase.Deps{DB: db, Secret: "test-secret", Gate: gate},
		Tree:   apitree.Deps{DB: db},
		Ticket: apiticket.Deps{DB: db},
	})

	if got := getAuth(engine, "/api/ticket/instances", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("未登录状态码 = %d", got.Code)
	}

	zhou := loginName(t, engine, "周宁", "secret")
	lin := loginName(t, engine, "林夏", "secret")
	xu := loginName(t, engine, "许衡", "secret")
	chen := loginName(t, engine, "陈舟", "secret")

	templates := decodeJSON[[]map[string]any](t, getAuth(engine, "/api/ticket/templates", chen))
	if len(templates) != 3 {
		t.Fatalf("模板数量 = %d", len(templates))
	}
	var templateID uint
	for _, item := range templates {
		if _, ok := item["status"]; ok {
			t.Fatal("模板上出现了状态")
		}
		if item["name"] == "变更申请" {
			templateID = uint(item["id"].(float64))
		}
	}
	if templateID == 0 {
		t.Fatal("没有变更申请模板")
	}

	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	trade := mustNode(t, nodes, "交易")

	if rec := postJSON(engine, "/api/ticket/instances", fmt.Sprintf(`{"templateId":%d,"treeNodeId":%d,"title":"周宁越权","status":"finished"}`, templateID, order.ID), zhou); rec.Code != http.StatusForbidden {
		t.Fatalf("周宁提交订单工单 = %d %s", rec.Code, rec.Body.String())
	}

	created := postJSON(engine, "/api/ticket/instances", fmt.Sprintf(`{"templateId":%d,"treeNodeId":%d,"title":"订单加字段","status":"finished"}`, templateID, order.ID), chen)
	if created.Code != http.StatusOK {
		t.Fatalf("陈舟提单 = %d %s", created.Code, created.Body.String())
	}
	var fresh struct {
		ID     uint   `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Status != "pending_approve" {
		t.Fatalf("新建状态 = %s", fresh.Status)
	}

	own := postJSON(engine, "/api/ticket/instances", fmt.Sprintf(`{"templateId":%d,"treeNodeId":%d,"title":"林夏自己的单"}`, templateID, order.ID), lin)
	if own.Code != http.StatusOK {
		t.Fatalf("林夏提单 = %d %s", own.Code, own.Body.String())
	}
	var ownID struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(own.Body.Bytes(), &ownID); err != nil {
		t.Fatal(err)
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", ownID.ID), "", lin); rec.Code != http.StatusForbidden {
		t.Fatalf("林夏审批自己 = %d %s", rec.Code, rec.Body.String())
	}

	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", fresh.ID), "", chen); rec.Code != http.StatusForbidden {
		t.Fatalf("陈舟审批 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", fresh.ID), "", zhou); rec.Code != http.StatusForbidden {
		t.Fatalf("周宁审批订单 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/finish", fresh.ID), "", lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("未审批就执行 = %d %s", rec.Code, rec.Body.String())
	}
	approved := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", fresh.ID), "", lin)
	if approved.Code != http.StatusOK {
		t.Fatalf("林夏审批 = %d %s", approved.Code, approved.Body.String())
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/finish", fresh.ID), "", chen); rec.Code != http.StatusForbidden {
		t.Fatalf("陈舟执行 = %d %s", rec.Code, rec.Body.String())
	}
	finished := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/finish", fresh.ID), "", lin)
	if finished.Code != http.StatusOK {
		t.Fatalf("林夏执行 = %d %s", finished.Code, finished.Body.String())
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", fresh.ID), "", lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("完成后再次审批 = %d %s", rec.Code, rec.Body.String())
	}

	second := postJSON(engine, "/api/ticket/instances", fmt.Sprintf(`{"templateId":%d,"treeNodeId":%d,"title":"订单回滚"}`, templateID, order.ID), chen)
	var secondID struct {
		ID uint `json:"id"`
	}
	if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &secondID) != nil {
		t.Fatalf("第二张单 = %d %s", second.Code, second.Body.String())
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/reject", secondID.ID), "", lin); rec.Code != http.StatusOK {
		t.Fatalf("林夏拒绝 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", secondID.ID), "", lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("拒绝后再审批 = %d %s", rec.Code, rec.Body.String())
	}

	scoped := decodeJSON[[]instanceViewJSON](t, getAuth(engine, fmt.Sprintf("/api/ticket/instances?nodeId=%d", trade.ID), lin))
	seen := map[string]bool{}
	for _, row := range scoped {
		if row.NodeName == "入口" || row.NodeName == "可观测" {
			t.Fatalf("交易子树带出了 %s", row.NodeName)
		}
		seen[row.Title] = true
	}
	if !seen["订单库升配"] || !seen["订单加字段"] || !seen["支付证书轮换"] {
		t.Fatalf("交易子树工单 = %+v", scoped)
	}
	all := decodeJSON[[]instanceViewJSON](t, getAuth(engine, "/api/ticket/instances", xu))
	foundEdge := false
	for _, row := range all {
		if row.Title == "入口扩容被拒绝" {
			foundEdge = true
		}
	}
	if foundEdge {
		t.Fatal("许衡看见了入口工单")
	}
}

type instanceViewJSON struct {
	Title    string `json:"title"`
	NodeName string `json:"nodeName"`
	Status   string `json:"status"`
}

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d %s", rec.Code, rec.Body.String())
	}
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
