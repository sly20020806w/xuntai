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
	apimonitor "xuntai/internal/api/monitor"
	apitask "xuntai/internal/api/task"
	apiticket "xuntai/internal/api/ticket"
	apitree "xuntai/internal/api/tree"
	"xuntai/internal/base"
	"xuntai/internal/model"
	"xuntai/internal/monitor"
	"xuntai/internal/task"
	"xuntai/internal/ticket"
	"xuntai/internal/tree"
)

func TestMonitorPullAndLeafDiscovery(t *testing.T) {
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
	if _, err := monitor.Seed(db); err != nil {
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
	})

	zhou := loginName(t, engine, "周宁", "secret")
	lin := loginName(t, engine, "林夏", "secret")
	xu := loginName(t, engine, "许衡", "secret")

	pools := decodeJSON[[]namedID](t, getAuth(engine, "/api/monitor/pools", xu))
	trade := mustNamed(t, pools, "交易采集")
	file := decodeJSON[pullFile](t, getAuth(engine, fmt.Sprintf("/api/monitor/pools/%d/targets", trade.ID), xu))
	if file.RemoteWrite != "远端存储" {
		t.Fatalf("远端写入 = %s", file.RemoteWrite)
	}
	got := map[string]bool{}
	for _, job := range file.Jobs {
		for _, target := range job.Targets {
			got[target] = true
		}
	}
	for _, target := range []string{"10.8.2.17:9256", "10.8.2.18:9256", "10.8.3.9:9256"} {
		if !got[target] {
			t.Fatalf("交易采集缺少 %s，现有 %v", target, got)
		}
	}
	if got["10.4.1.21:9100"] {
		t.Fatal("交易采集带出了可观测的目标")
	}

	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	parent := mustNode(t, nodes, "交易")
	jobBody := fmt.Sprintf(`{"poolId":%d,"treeNodeId":%d,"name":"订单补充","port":9256}`, trade.ID, order.ID)
	if rec := postJSON(engine, "/api/monitor/jobs", jobBody, zhou); rec.Code != http.StatusForbidden {
		t.Fatalf("周宁建订单采集 = %d %s", rec.Code, rec.Body.String())
	}
	leafBody := fmt.Sprintf(`{"poolId":%d,"treeNodeId":%d,"name":"挂在父节点","discover":"tree","port":9256}`, trade.ID, parent.ID)
	if rec := postJSON(engine, "/api/monitor/jobs", leafBody, lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("父节点服务树发现 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/monitor/jobs", fmt.Sprintf(`{"poolId":%d,"treeNodeId":%d,"name":"交易集群外","discover":"k8s"}`, trade.ID, parent.ID), lin); rec.Code != http.StatusOK {
		t.Fatalf("集群发现 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/monitor/jobs", jobBody, lin); rec.Code != http.StatusOK {
		t.Fatalf("林夏建订单采集 = %d %s", rec.Code, rec.Body.String())
	}
	jobs := decodeJSON[[]namedID](t, getAuth(engine, "/api/monitor/jobs", lin))
	if _, ok := findNamed(jobs, "订单进程"); !ok {
		t.Fatal("林夏看不见订单采集")
	}
	hiddenJobs := decodeJSON[[]namedID](t, getAuth(engine, "/api/monitor/jobs", xu))
	if _, ok := findNamed(hiddenJobs, "订单进程"); ok {
		t.Fatal("许衡看见了订单采集")
	}
	if _, ok := findNamed(jobs, "订单补充"); !ok {
		t.Fatal("新采集任务没有出现在列表里")
	}

	groups := decodeJSON[[]sendGroupJSON](t, getAuth(engine, "/api/monitor/send-groups", lin))
	tradeGroup := mustSend(t, groups, "交易发送")
	if tradeGroup.SendGroupID != tradeGroup.ID {
		t.Fatalf("发送组编号 = %+v", tradeGroup)
	}
	rules := decodeJSON[[]ruleJSON](t, getAuth(engine, "/api/monitor/rules", lin))
	orderRule := mustRule(t, rules, "订单错误率")
	if orderRule.SendGroupID != tradeGroup.ID || orderRule.Status != "firing" {
		t.Fatalf("订单错误率 = %+v", orderRule)
	}

	quiet := postJSON(engine, "/api/monitor/pools", `{"name":"只采集","remoteWrite":"远端存储","supportAlert":false}`, zhou)
	if quiet.Code != http.StatusOK {
		t.Fatalf("只采集池 = %d %s", quiet.Code, quiet.Body.String())
	}
	var quietID struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(quiet.Body.Bytes(), &quietID); err != nil {
		t.Fatal(err)
	}
	ruleBody := fmt.Sprintf(`{"name":"不该有的规则","expr":"up == 0","level":"警告","poolId":%d,"sendGroupId":%d}`, quietID.ID, tradeGroup.ID)
	if rec := postJSON(engine, "/api/monitor/rules", ruleBody, lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("无告警池建规则 = %d %s", rec.Code, rec.Body.String())
	}
}

func TestMonitorObjectBinding(t *testing.T) {
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
	if _, err := monitor.Seed(db); err != nil {
		t.Fatal(err)
	}
	gate := access.New()
	if err := gate.Reload(db); err != nil {
		t.Fatal(err)
	}
	engine := New(Deps{
		Base:    apibase.Deps{DB: db, Secret: "test-secret", Gate: gate},
		Tree:    apitree.Deps{DB: db},
		Monitor: apimonitor.Deps{DB: db},
	})
	lin := loginName(t, engine, "林夏", "secret")
	xu := loginName(t, engine, "许衡", "secret")
	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	pay := mustNode(t, nodes, "支付")
	var observe model.Node
	if err := db.Where("name = ?", "可观测").First(&observe).Error; err != nil {
		t.Fatal(err)
	}
	cmdb := model.CMDBModel{Name: "服务", Code: "svc"}
	if err := db.Create(&cmdb).Error; err != nil {
		t.Fatal(err)
	}
	orderObj := model.CMDBObject{ModelID: cmdb.ID, Name: "订单服务", TreeNodeID: order.ID}
	payObj := model.CMDBObject{ModelID: cmdb.ID, Name: "支付服务", TreeNodeID: pay.ID}
	linked := model.CMDBObject{ModelID: cmdb.ID, Name: "挂在订单", TreeNodeID: 0}
	if err := db.Create(&orderObj).Error; err != nil || db.Create(&payObj).Error != nil || db.Create(&linked).Error != nil {
		t.Fatal("对象没有建成")
	}
	if err := db.Create(&model.ObjectNode{ObjectID: linked.ID, NodeID: order.ID}).Error; err != nil {
		t.Fatal(err)
	}
	orderProject := model.Project{Name: "订单项目", TreeNodeID: order.ID}
	payProject := model.Project{Name: "支付项目", TreeNodeID: pay.ID}
	if err := db.Create(&orderProject).Error; err != nil || db.Create(&payProject).Error != nil {
		t.Fatal("项目没有建成")
	}
	orderApp := model.App{ProjectID: orderProject.ID, Name: "order-api"}
	payApp := model.App{ProjectID: payProject.ID, Name: "pay-api"}
	if err := db.Create(&orderApp).Error; err != nil || db.Create(&payApp).Error != nil {
		t.Fatal("应用没有建成")
	}
	pools := decodeJSON[[]namedID](t, getAuth(engine, "/api/monitor/pools", lin))
	pool := mustNamed(t, pools, "交易采集")
	groups := decodeJSON[[]boundGroup](t, getAuth(engine, "/api/monitor/send-groups", lin))
	tradeGroup := mustBoundGroup(t, groups, "交易发送")
	if tradeGroup.NodeName != "交易" {
		t.Fatalf("交易发送节点 = %+v", tradeGroup)
	}
	hiddenGroups := decodeJSON[[]boundGroup](t, getAuth(engine, "/api/monitor/send-groups", xu))
	if _, ok := findBoundGroup(hiddenGroups, "交易发送"); ok {
		t.Fatal("许衡看见了交易发送")
	}

	badObject := fmt.Sprintf(`{"name":"错对象","expr":"up == 0","level":"警告","poolId":%d,"sendGroupId":%d,"treeNodeId":%d,"objectId":%d}`, pool.ID, tradeGroup.ID, order.ID, payObj.ID)
	if rec := postJSON(engine, "/api/monitor/rules", badObject, lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("对象不在节点下 = %d %s", rec.Code, rec.Body.String())
	}
	badApp := fmt.Sprintf(`{"name":"错应用","expr":"up == 0","level":"警告","poolId":%d,"sendGroupId":%d,"treeNodeId":%d,"appId":%d}`, pool.ID, tradeGroup.ID, order.ID, payApp.ID)
	if rec := postJSON(engine, "/api/monitor/rules", badApp, lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("应用不在节点下 = %d %s", rec.Code, rec.Body.String())
	}
	good := fmt.Sprintf(`{"name":"订单对象规则","expr":"up == 0","level":"紧急","poolId":%d,"sendGroupId":%d,"treeNodeId":%d,"objectId":%d,"appId":%d}`, pool.ID, tradeGroup.ID, order.ID, orderObj.ID, orderApp.ID)
	created := postJSON(engine, "/api/monitor/rules", good, lin)
	if created.Code != http.StatusOK {
		t.Fatalf("绑定对象规则 = %d %s", created.Code, created.Body.String())
	}
	var createdID struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdID); err != nil {
		t.Fatal(err)
	}
	linkedRule := fmt.Sprintf(`{"name":"关系对象规则","expr":"up == 0","level":"警告","poolId":%d,"sendGroupId":%d,"treeNodeId":%d,"objectId":%d}`, pool.ID, tradeGroup.ID, order.ID, linked.ID)
	if rec := postJSON(engine, "/api/monitor/rules", linkedRule, lin); rec.Code != http.StatusOK {
		t.Fatalf("关系表对象 = %d %s", rec.Code, rec.Body.String())
	}
	rules := decodeJSON[[]boundRule](t, getAuth(engine, "/api/monitor/rules", lin))
	gotRule := mustBoundRule(t, rules, "订单对象规则")
	if gotRule.NodeID != order.ID || gotRule.NodeName != "订单" || gotRule.ObjectID != orderObj.ID || gotRule.ObjectName != "订单服务" || gotRule.AppID != orderApp.ID {
		t.Fatalf("规则归属 = %+v", gotRule)
	}
	if _, ok := findBoundRule(decodeJSON[[]boundRule](t, getAuth(engine, "/api/monitor/rules", xu)), "订单对象规则"); ok {
		t.Fatal("许衡看见了订单对象规则")
	}
	moved := fmt.Sprintf(`{"name":"订单对象规则","expr":"up == 0","level":"紧急","poolId":%d,"sendGroupId":%d,"treeNodeId":%d,"objectId":%d}`, pool.ID, tradeGroup.ID, order.ID, payObj.ID)
	if rec := putJSON(engine, fmt.Sprintf("/api/monitor/rules/%d", createdID.ID), moved, lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("改到别的对象 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := putJSON(engine, fmt.Sprintf("/api/monitor/rules/%d", createdID.ID), moved, xu); rec.Code != http.StatusForbidden {
		t.Fatalf("许衡改订单规则 = %d %s", rec.Code, rec.Body.String())
	}

	badGroup := fmt.Sprintf(`{"name":"错发送组","treeNodeId":%d,"objectId":%d}`, order.ID, payObj.ID)
	if rec := postJSON(engine, "/api/monitor/send-groups", badGroup, lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("发送组对象 = %d %s", rec.Code, rec.Body.String())
	}
	groupBody := fmt.Sprintf(`{"name":"订单发送","treeNodeId":%d,"objectId":%d}`, order.ID, orderObj.ID)
	groupRec := postJSON(engine, "/api/monitor/send-groups", groupBody, lin)
	if groupRec.Code != http.StatusOK {
		t.Fatalf("订单发送 = %d %s", groupRec.Code, groupRec.Body.String())
	}
	var groupID struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(groupRec.Body.Bytes(), &groupID); err != nil {
		t.Fatal(err)
	}
	listed := decodeJSON[[]boundGroup](t, getAuth(engine, "/api/monitor/send-groups", lin))
	gotGroup := mustBoundGroup(t, listed, "订单发送")
	if gotGroup.NodeID != order.ID || gotGroup.NodeName != "订单" || gotGroup.ObjectID != orderObj.ID || gotGroup.ObjectName != "订单服务" {
		t.Fatalf("发送组归属 = %+v", gotGroup)
	}
	if _, ok := findBoundGroup(decodeJSON[[]boundGroup](t, getAuth(engine, "/api/monitor/send-groups", xu)), "订单发送"); ok {
		t.Fatal("许衡看见了订单发送")
	}
	retarget := fmt.Sprintf(`{"name":"订单发送","treeNodeId":%d}`, observe.ID)
	if rec := putJSON(engine, fmt.Sprintf("/api/monitor/send-groups/%d", groupID.ID), retarget, xu); rec.Code != http.StatusForbidden {
		t.Fatalf("许衡改订单发送组 = %d %s", rec.Code, rec.Body.String())
	}

	alert := fmt.Sprintf(`{"fingerprint":"order-obj","nodeId":%d,"objectId":%d}`, order.ID, orderObj.ID)
	if rec := postJSON(engine, "/api/monitor/alerts/webhook", alert, xu); rec.Code != http.StatusForbidden {
		t.Fatalf("许衡按对象找人 = %d %s", rec.Code, rec.Body.String())
	}
	hook := postJSON(engine, "/api/monitor/alerts/webhook", alert, lin)
	if hook.Code != http.StatusOK {
		t.Fatalf("林夏按对象找人 = %d %s", hook.Code, hook.Body.String())
	}
	var owners struct {
		Owners []struct {
			Name string `json:"name"`
		} `json:"owners"`
	}
	if err := json.Unmarshal(hook.Body.Bytes(), &owners); err != nil {
		t.Fatal(err)
	}
	if len(owners.Owners) == 0 || owners.Owners[0].Name != "林夏" {
		t.Fatalf("负责人 = %+v", owners.Owners)
	}
	for _, path := range []string{"/api/monitor/alerts/assign", "/api/monitor/alerts/mute", "/api/monitor/alerts/escalate"} {
		if rec := postJSON(engine, path, alert, lin); rec.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, rec.Code, rec.Body.String())
		}
	}
	actions := decodeJSON[[]actionJSON](t, getAuth(engine, fmt.Sprintf("/api/monitor/alerts/actions?objectId=%d", orderObj.ID), lin))
	seen := map[string]bool{}
	for _, row := range actions {
		if row.ObjectName != "订单服务" || row.OwnerName != "林夏" {
			t.Fatalf("处理记录 = %+v", row)
		}
		seen[row.Action] = true
	}
	for _, action := range []string{"assign", "mute", "escalate"} {
		if !seen[action] {
			t.Fatalf("缺少 %s，现有 %v", action, seen)
		}
	}
	if rows := decodeJSON[[]actionJSON](t, getAuth(engine, "/api/monitor/alerts/actions", xu)); len(rows) != 0 {
		t.Fatalf("许衡看见了订单处理 %d 条", len(rows))
	}
}

type boundRule struct {
	Name       string `json:"name"`
	NodeID     uint   `json:"nodeId"`
	NodeName   string `json:"nodeName"`
	ObjectID   uint   `json:"objectId"`
	ObjectName string `json:"objectName"`
	AppID      uint   `json:"appId"`
}

type boundGroup struct {
	ID         uint   `json:"id"`
	Name       string `json:"name"`
	NodeID     uint   `json:"nodeId"`
	NodeName   string `json:"nodeName"`
	ObjectID   uint   `json:"objectId"`
	ObjectName string `json:"objectName"`
}

type actionJSON struct {
	Action     string `json:"action"`
	ObjectName string `json:"objectName"`
	OwnerName  string `json:"ownerName"`
}

func mustBoundRule(t *testing.T, rows []boundRule, name string) boundRule {
	t.Helper()
	row, ok := findBoundRule(rows, name)
	if !ok {
		t.Fatalf("没有规则 %s", name)
	}
	return row
}

func findBoundRule(rows []boundRule, name string) (boundRule, bool) {
	for _, row := range rows {
		if row.Name == name {
			return row, true
		}
	}
	return boundRule{}, false
}

func mustBoundGroup(t *testing.T, rows []boundGroup, name string) boundGroup {
	t.Helper()
	row, ok := findBoundGroup(rows, name)
	if !ok {
		t.Fatalf("没有发送组 %s", name)
	}
	return row
}

func findBoundGroup(rows []boundGroup, name string) (boundGroup, bool) {
	for _, row := range rows {
		if row.Name == name {
			return row, true
		}
	}
	return boundGroup{}, false
}

type namedID struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type pullFile struct {
	RemoteWrite string `json:"remoteWrite"`
	Jobs        []struct {
		Targets []string `json:"targets"`
	} `json:"jobs"`
}

type sendGroupJSON struct {
	ID          uint   `json:"id"`
	SendGroupID uint   `json:"sendgroup_id"`
	Name        string `json:"name"`
}

type ruleJSON struct {
	Name        string `json:"name"`
	SendGroupID uint   `json:"sendgroup_id"`
	Status      string `json:"status"`
}

func mustNamed(t *testing.T, rows []namedID, name string) namedID {
	t.Helper()
	row, ok := findNamed(rows, name)
	if !ok {
		t.Fatalf("没有 %s", name)
	}
	return row
}

func findNamed(rows []namedID, name string) (namedID, bool) {
	for _, row := range rows {
		if row.Name == name {
			return row, true
		}
	}
	return namedID{}, false
}

func mustSend(t *testing.T, rows []sendGroupJSON, name string) sendGroupJSON {
	t.Helper()
	for _, row := range rows {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("没有发送组 %s", name)
	return sendGroupJSON{}
}

func mustRule(t *testing.T, rows []ruleJSON, name string) ruleJSON {
	t.Helper()
	for _, row := range rows {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("没有规则 %s", name)
	return ruleJSON{}
}
