package httpserver

import (
	"bytes"
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
	apitree "xuntai/internal/api/tree"
	"xuntai/internal/base"
	"xuntai/internal/model"
	"xuntai/internal/tree"
)

func TestTreeLeafAndOpsBoundary(t *testing.T) {
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
	gate := access.New()
	if err := gate.Reload(db); err != nil {
		t.Fatal(err)
	}
	engine := New(Deps{
		Base: apibase.Deps{DB: db, Secret: "test-secret", Gate: gate},
		Tree: apitree.Deps{DB: db},
	})

	if got := getAuth(engine, "/api/tree/nodes", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("未登录状态码 = %d", got.Code)
	}

	zhou := loginName(t, engine, "周宁", "secret")
	lin := loginName(t, engine, "林夏", "secret")
	xu := loginName(t, engine, "许衡", "secret")
	chen := loginName(t, engine, "陈舟", "secret")

	me := getAuth(engine, "/api/base/me", lin)
	var profile struct {
		Menus []struct {
			Path string `json:"path"`
		} `json:"menus"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if len(profile.Menus) != 1 || profile.Menus[0].Path != "tree" {
		t.Fatalf("林夏的菜单 = %+v", profile.Menus)
	}

	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", xu))
	if !hasNode(nodes, "交易") || !hasNode(nodes, "可观测") {
		t.Fatal("列表按负责人收窄了")
	}
	order := mustNode(t, nodes, "订单")
	pay := mustNode(t, nodes, "支付")
	observe := mustNode(t, nodes, "可观测")
	edge := mustNode(t, nodes, "入口")
	arch := mustNode(t, nodes, "基础架构")
	trade := mustNode(t, nodes, "交易")

	if code := postJSON(engine, fmt.Sprintf("/api/tree/nodes/%d/machines", edge.ID), `{"name":"edge-x","ip":"10.0.0.1"}`, xu).Code; code != http.StatusForbidden {
		t.Fatalf("许衡挂入口机器状态码 = %d", code)
	}
	if code := postJSON(engine, fmt.Sprintf("/api/tree/nodes/%d/machines", order.ID), `{"name":"order-x","ip":"10.0.0.2"}`, zhou).Code; code != http.StatusForbidden {
		t.Fatalf("周宁挂订单机器状态码 = %d", code)
	}
	if code := postJSON(engine, fmt.Sprintf("/api/tree/nodes/%d/machines", order.ID), `{"name":"order-y","ip":"10.0.0.3"}`, chen).Code; code != http.StatusForbidden {
		t.Fatalf("陈舟挂订单机器状态码 = %d", code)
	}
	if code := putJSON(engine, fmt.Sprintf("/api/tree/nodes/%d/owners", order.ID), `{"owners":[]}`, chen).Code; code != http.StatusForbidden {
		t.Fatalf("陈舟改负责人状态码 = %d", code)
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/tree/nodes/%d/machines", arch.ID), `{"name":"arch-x","ip":"10.0.0.4"}`, zhou); rec.Code != http.StatusBadRequest {
		t.Fatalf("非叶子挂机器 = %d %s", rec.Code, rec.Body.String())
	}

	tradeMachines := decodeMachines(t, getAuth(engine, fmt.Sprintf("/api/tree/machines?nodeId=%d", trade.ID), xu))
	if len(tradeMachines) != 3 {
		t.Fatalf("交易子树机器数量 = %d", len(tradeMachines))
	}
	for _, machine := range tradeMachines {
		if machine.NodeName == "可观测" {
			t.Fatal("交易子树带出了可观测的机器")
		}
	}

	bound := postJSON(engine, fmt.Sprintf("/api/tree/nodes/%d/machines", observe.ID), `{"name":"obs-a-03","ip":"10.4.1.23","vendor":"自建"}`, zhou)
	if bound.Code != http.StatusOK {
		t.Fatalf("周宁挂可观测机器 = %d %s", bound.Code, bound.Body.String())
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/tree/nodes/%d/machines", order.ID), `{"name":"order-c-09","ip":"10.8.2.19","vendor":"公有云"}`, lin); rec.Code != http.StatusOK {
		t.Fatalf("林夏挂订单机器 = %d %s", rec.Code, rec.Body.String())
	}

	users := decodeUsers(t, getAuth(engine, "/api/base/users", zhou))
	linID := userID(t, users, "林夏")
	chenID := userID(t, users, "陈舟")
	replaced := putJSON(engine, fmt.Sprintf("/api/tree/nodes/%d/owners", pay.ID), fmt.Sprintf(`{"owners":[{"userId":%d,"kind":"ops"},{"userId":%d,"kind":"rd"}]}`, linID, chenID), lin)
	if replaced.Code != http.StatusOK {
		t.Fatalf("林夏改支付负责人 = %d %s", replaced.Code, replaced.Body.String())
	}
	payOwners := mustNode(t, decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin)), "支付")
	if len(payOwners.Owners) != 2 {
		t.Fatalf("支付负责人 = %+v", payOwners.Owners)
	}

	child := postJSON(engine, "/api/tree/nodes", fmt.Sprintf(`{"name":"退款","parentId":%d}`, trade.ID), lin)
	if child.Code != http.StatusOK {
		t.Fatalf("林夏建退款 = %d %s", child.Code, child.Body.String())
	}
	var created struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(child.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	nested := postJSON(engine, "/api/tree/nodes", fmt.Sprintf(`{"name":"退款审核","parentId":%d}`, created.ID), lin)
	if nested.Code != http.StatusOK {
		t.Fatalf("空叶子下建子节点 = %d %s", nested.Code, nested.Body.String())
	}
	refund := mustNode(t, decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin)), "退款")
	if refund.IsLeaf {
		t.Fatal("退款挂了子节点后仍是叶子")
	}
	review := mustNode(t, decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin)), "退款审核")
	if rec := postJSON(engine, fmt.Sprintf("/api/tree/nodes/%d/machines", refund.ID), `{"name":"refund-a","ip":"10.9.0.1"}`, lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("非叶子退款挂机器 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/tree/nodes/%d/machines", review.ID), `{"name":"refund-b","ip":"10.9.0.2"}`, lin); rec.Code != http.StatusOK {
		t.Fatalf("退款审核挂机器 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/tree/nodes", fmt.Sprintf(`{"name":"再下一层","parentId":%d}`, review.ID), lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("有机器的叶子再建子节点 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/tree/nodes", fmt.Sprintf(`{"name":"观测子节点","parentId":%d}`, observe.ID), xu); rec.Code != http.StatusBadRequest {
		t.Fatalf("可观测再建子节点 = %d %s", rec.Code, rec.Body.String())
	}
}

func loginName(t *testing.T, engine http.Handler, name, password string) string {
	t.Helper()
	rec := postJSON(engine, "/api/base/login", fmt.Sprintf(`{"name":"%s","password":"%s"}`, name, password), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("登录 %s = %d %s", name, rec.Code, rec.Body.String())
	}
	var session struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	return session.Token
}

func putJSON(engine http.Handler, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

type treeNode struct {
	ID     uint `json:"id"`
	Name   string
	IsLeaf bool `json:"isLeaf"`
	Owners []struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"owners"`
}

type treeMachine struct {
	Name     string `json:"name"`
	NodeName string `json:"nodeName"`
}

type treeUser struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

func decodeNodes(t *testing.T, rec *httptest.ResponseRecorder) []treeNode {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("节点列表 = %d %s", rec.Code, rec.Body.String())
	}
	var nodes []treeNode
	if err := json.Unmarshal(rec.Body.Bytes(), &nodes); err != nil {
		t.Fatal(err)
	}
	return nodes
}

func decodeMachines(t *testing.T, rec *httptest.ResponseRecorder) []treeMachine {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("机器列表 = %d %s", rec.Code, rec.Body.String())
	}
	var machines []treeMachine
	if err := json.Unmarshal(rec.Body.Bytes(), &machines); err != nil {
		t.Fatal(err)
	}
	return machines
}

func decodeUsers(t *testing.T, rec *httptest.ResponseRecorder) []treeUser {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("用户列表 = %d %s", rec.Code, rec.Body.String())
	}
	var users []treeUser
	if err := json.Unmarshal(rec.Body.Bytes(), &users); err != nil {
		t.Fatal(err)
	}
	return users
}

func hasNode(nodes []treeNode, name string) bool {
	for _, node := range nodes {
		if node.Name == name {
			return true
		}
	}
	return false
}

func mustNode(t *testing.T, nodes []treeNode, name string) treeNode {
	t.Helper()
	for _, node := range nodes {
		if node.Name == name {
			return node
		}
	}
	t.Fatalf("没有节点 %s", name)
	return treeNode{}
}

func userID(t *testing.T, users []treeUser, name string) uint {
	t.Helper()
	for _, user := range users {
		if user.Name == name {
			return user.ID
		}
	}
	t.Fatalf("没有用户 %s", name)
	return 0
}
