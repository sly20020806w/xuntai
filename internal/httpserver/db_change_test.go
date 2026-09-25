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
	apidb "xuntai/internal/api/db"
	apimonitor "xuntai/internal/api/monitor"
	apiplay "xuntai/internal/api/playbook"
	apitask "xuntai/internal/api/task"
	apiticket "xuntai/internal/api/ticket"
	apitree "xuntai/internal/api/tree"
	"xuntai/internal/base"
	dbcore "xuntai/internal/db"
	"xuntai/internal/model"
	"xuntai/internal/monitor"
	"xuntai/internal/playbook"
	"xuntai/internal/task"
	"xuntai/internal/ticket"
	"xuntai/internal/tree"
)

func TestDbChangeAndScrape(t *testing.T) {
	t.Setenv("XUNTAI_TASK_MOCK", "1")
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
		func(db *gorm.DB) error { _, err := dbcore.Seed(db); return err },
		func(db *gorm.DB) error { _, err := monitor.Seed(db); return err },
		func(db *gorm.DB) error { _, err := playbook.Seed(db); return err },
	} {
		if err := seed(db); err != nil {
			t.Fatal(err)
		}
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
		Monitor:  apimonitor.Deps{DB: db},
		Db:       apidb.Deps{DB: db},
		Playbook: apiplay.Deps{DB: db},
	})
	lin := loginName(t, engine, "林夏", "secret")
	zhou := loginName(t, engine, "周宁", "secret")
	xu := loginName(t, engine, "许衡", "secret")
	chen := loginName(t, engine, "陈舟", "secret")
	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	instances := decodeJSON[[]dbInstanceJSON](t, getAuth(engine, "/api/db/instances", lin))
	master := mustDbInstance(t, instances, "订单主库")

	if rec := postJSON(engine, "/api/db/instances", fmt.Sprintf(`{"name":"订单开发库","treeNodeId":%d,"host":"10.8.2.17","port":3308,"version":"8.0","role":"从","masterId":%d,"env":"开发","loginUser":"app","monitorAddr":"10.8.2.17:19104"}`, order.ID, master.ID), xu); rec.Code != http.StatusForbidden {
		t.Fatalf("许衡登记 = %d %s", rec.Code, rec.Body.String())
	}
	created := postJSON(engine, "/api/db/instances", fmt.Sprintf(`{"name":"订单开发库","treeNodeId":%d,"host":"10.8.2.17","port":3308,"version":"8.0","role":"从","masterId":%d,"env":"开发","loginUser":"app","monitorAddr":"10.8.2.17:19104"}`, order.ID, master.ID), zhou)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"objectId"`) {
		t.Fatalf("周宁登记开发库 = %d %s", created.Code, created.Body.String())
	}
	devID := decodeJSON[struct {
		ID       uint `json:"id"`
		ObjectID uint `json:"objectId"`
	}](t, created)
	if devID.ID == 0 || devID.ObjectID == 0 {
		t.Fatalf("开发库没有对象 %+v", devID)
	}

	templates := decodeJSON[[]namedID](t, getAuth(engine, "/api/ticket/templates", chen))
	tpl := templateID(t, templates, "建库")
	pending := openDBTicket(t, engine, chen, tpl, order.ID, "还没批的建库", map[string]any{
		"instance_id": devID.ID, "action": "create_database", "database": "appdb",
	})
	start := fmt.Sprintf(`{"playbook":"db.change","idempotencyKey":"db-unapproved","input":{"ticket_id":%d,"tree_node_id":%d,"instance_id":%d,"action":"create_database","database":"appdb"}}`, pending, order.ID, devID.ID)
	unapproved := postJSON(engine, "/api/playbook/runs", start, lin)
	if unapproved.Code != http.StatusOK {
		t.Fatalf("未审批启动 = %d %s", unapproved.Code, unapproved.Body.String())
	}
	early := decodeRun(t, getAuth(engine, fmt.Sprintf("/api/playbook/runs/%d", decodeRun(t, unapproved).ID), lin))
	if early.Status != "failed" || !strings.Contains(early.Steps[0].Error, "审批") {
		t.Fatalf("未审批仍执行了 %+v", early)
	}

	approved := approveDB(t, engine, lin, openDBTicket(t, engine, chen, tpl, order.ID, "开发建库", map[string]any{
		"instance_id": devID.ID, "action": "create_database", "database": "appdb",
	}))
	run := decodeRun(t, getAuth(engine, fmt.Sprintf("/api/playbook/runs/%d", approved.RunID), lin))
	if run.Status != "success" || run.Steps[0].Output["statement"] != "CREATE DATABASE appdb" {
		t.Fatalf("建库 = %+v", run)
	}
	jobs := decodeJSON[[]jobJSON](t, getAuth(engine, "/api/task/jobs", lin))
	built := mustJob(t, jobs, "建库")
	if built.Status != "finished" || built.Done != built.Total {
		t.Fatalf("建库任务 = %+v", built)
	}
	if ticketStatus(t, engine, lin, approved.TicketID) != "finished" {
		t.Fatal("成功的变更没有收完工单")
	}

	accountTpl := templateID(t, templates, "建账号")
	failedTicket := openDBTicket(t, engine, chen, accountTpl, order.ID, "建账号失败", map[string]any{
		"instance_id": devID.ID, "action": "create_account", "account": "app", "account_host": "10.8.2.17", "task_mock": "failed",
	})
	failedRunID := approveDB(t, engine, lin, failedTicket).RunID
	failedRun := decodeRun(t, getAuth(engine, fmt.Sprintf("/api/playbook/runs/%d", failedRunID), lin))
	if failedRun.Status != "failed" {
		t.Fatalf("失败的建账号 = %+v", failedRun)
	}
	jobs = decodeJSON[[]jobJSON](t, getAuth(engine, "/api/task/jobs", lin))
	accountJob := mustJob(t, jobs, "建账号")
	if accountJob.Status != "finished" || accountJob.Done != 1 {
		t.Fatalf("失败结果没有记下 %+v", accountJob)
	}
	if ticketStatus(t, engine, lin, failedTicket) != "pending_action" {
		t.Fatal("失败把工单收完了")
	}

	sqlTpl := templateID(t, templates, "SQL变更")
	dropID := approveDB(t, engine, lin, openDBTicket(t, engine, chen, sqlTpl, order.ID, "删表", map[string]any{
		"instance_id": devID.ID, "action": "sql_change", "sql": "DROP TABLE orders",
	})).RunID
	dropRun := decodeRun(t, getAuth(engine, fmt.Sprintf("/api/playbook/runs/%d", dropID), lin))
	if dropRun.Status != "failed" || !strings.Contains(dropRun.Steps[0].Error, "白名单") {
		t.Fatalf("危险 SQL = %+v", dropRun)
	}

	t.Setenv("XUNTAI_TASK_MOCK", "")
	before := len(decodeJSON[[]jobJSON](t, getAuth(engine, "/api/task/jobs", lin)))
	prodID := approveDB(t, engine, lin, openDBTicket(t, engine, chen, tpl, order.ID, "生产建库", map[string]any{
		"instance_id": master.ID, "action": "create_database", "database": "appdb",
	})).RunID
	prodRun := decodeRun(t, getAuth(engine, fmt.Sprintf("/api/playbook/runs/%d", prodID), lin))
	if prodRun.Status != "failed" || !strings.Contains(prodRun.Steps[0].Error, "生产实例不走真实执行") {
		t.Fatalf("生产真实执行 = %+v", prodRun)
	}
	if len(decodeJSON[[]jobJSON](t, getAuth(engine, "/api/task/jobs", lin))) != before {
		t.Fatal("生产实例仍然下发了任务")
	}

	pools := decodeJSON[[]struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}](t, getAuth(engine, "/api/monitor/pools", lin))
	var poolID uint
	for _, pool := range pools {
		if pool.Name == "交易采集" {
			poolID = pool.ID
		}
	}
	if poolID == 0 {
		t.Fatal("没有交易采集池")
	}
	job := postJSON(engine, "/api/monitor/jobs", fmt.Sprintf(`{"poolId":%d,"treeNodeId":%d,"name":"订单库指标","discover":"db"}`, poolID, order.ID), lin)
	if job.Code != http.StatusOK {
		t.Fatalf("采集任务 = %d %s", job.Code, job.Body.String())
	}
	pulled := getAuth(engine, fmt.Sprintf("/api/monitor/pools/%d/targets", poolID), lin)
	body := pulled.Body.String()
	if pulled.Code != http.StatusOK || !strings.Contains(body, "10.8.2.17:9104") || !strings.Contains(body, "10.8.2.18:9104") || !strings.Contains(body, "10.8.2.17:19104") {
		t.Fatalf("采集目标 = %d %s", pulled.Code, body)
	}
	group := postJSON(engine, "/api/monitor/send-groups", fmt.Sprintf(`{"name":"订单库发送","treeNodeId":%d,"objectId":%d}`, order.ID, devID.ObjectID), lin)
	if group.Code != http.StatusOK {
		t.Fatalf("发送组 = %d %s", group.Code, group.Body.String())
	}
	groupID := decodeJSON[struct {
		ID uint `json:"id"`
	}](t, group).ID
	rule := postJSON(engine, "/api/monitor/rules", fmt.Sprintf(`{"name":"订单库失联","expr":"mysql_up == 0","level":"警告","poolId":%d,"sendGroupId":%d,"treeNodeId":%d,"objectId":%d}`, poolID, groupID, order.ID, master.ObjectID), lin)
	if rule.Code != http.StatusOK {
		t.Fatalf("规则 = %d %s", rule.Code, rule.Body.String())
	}
}

func templateID(t *testing.T, rows []namedID, name string) uint {
	t.Helper()
	for _, row := range rows {
		if row.Name == name {
			return row.ID
		}
	}
	t.Fatalf("没有模板 %s", name)
	return 0
}

func openDBTicket(t *testing.T, engine http.Handler, token string, templateID, nodeID uint, title string, payload map[string]any) uint {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"templateId": templateID, "treeNodeId": nodeID, "title": title, "payload": string(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := postJSON(engine, "/api/ticket/instances", string(body), token)
	if rec.Code != http.StatusOK {
		t.Fatalf("提单 %s = %d %s", title, rec.Code, rec.Body.String())
	}
	return decodeJSON[struct {
		ID uint `json:"id"`
	}](t, rec).ID
}

type approvedTicket struct {
	TicketID uint
	RunID    uint
}

func approveDB(t *testing.T, engine http.Handler, token string, ticketID uint) approvedTicket {
	t.Helper()
	rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", ticketID), "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("审批 %d = %d %s", ticketID, rec.Code, rec.Body.String())
	}
	body := decodeJSON[struct {
		ID    uint `json:"id"`
		RunID uint `json:"runId"`
	}](t, rec)
	if body.RunID == 0 {
		t.Fatalf("审批没有启动执行 %s", rec.Body.String())
	}
	return approvedTicket{TicketID: body.ID, RunID: body.RunID}
}

func ticketStatus(t *testing.T, engine http.Handler, token string, id uint) string {
	t.Helper()
	rows := decodeJSON[[]struct {
		ID     uint   `json:"id"`
		Status string `json:"status"`
	}](t, getAuth(engine, "/api/ticket/instances", token))
	for _, row := range rows {
		if row.ID == id {
			return row.Status
		}
	}
	t.Fatalf("没有工单 %d", id)
	return ""
}
