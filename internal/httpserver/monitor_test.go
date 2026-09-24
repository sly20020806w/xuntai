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
	jobs := decodeJSON[[]namedID](t, getAuth(engine, "/api/monitor/jobs", xu))
	if _, ok := findNamed(jobs, "订单进程"); !ok {
		t.Fatal("列表按负责人收窄了")
	}
	if _, ok := findNamed(jobs, "订单补充"); !ok {
		t.Fatal("新采集任务没有出现在列表里")
	}

	groups := decodeJSON[[]sendGroupJSON](t, getAuth(engine, "/api/monitor/send-groups", lin))
	tradeGroup := mustSend(t, groups, "交易发送")
	if tradeGroup.SendGroupID != tradeGroup.ID {
		t.Fatalf("发送组编号 = %+v", tradeGroup)
	}
	rules := decodeJSON[[]ruleJSON](t, getAuth(engine, "/api/monitor/rules", xu))
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
