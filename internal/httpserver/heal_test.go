package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"xuntai/internal/access"
	apibase "xuntai/internal/api/base"
	apimonitor "xuntai/internal/api/monitor"
	apiplay "xuntai/internal/api/playbook"
	apitree "xuntai/internal/api/tree"
	"xuntai/internal/base"
	"xuntai/internal/model"
	"xuntai/internal/monitor"
	"xuntai/internal/playbook"
	"xuntai/internal/tree"
)

func TestAlertHeal(t *testing.T) {
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
		func(db *gorm.DB) error { _, err := monitor.Seed(db); return err },
		func(db *gorm.DB) error { _, err := playbook.Seed(db); return err },
	} {
		if err := seed(db); err != nil {
			t.Fatal(err)
		}
	}
	note := mustHealBook(t, db, "heal.note", "告警记一笔", "published", "wait_manual")
	failBook := mustHealBook(t, db, "heal.fail", "告警失败", "published", "broken")
	draft := mustHealBook(t, db, "heal.draft", "告警草稿", "draft", "wait_manual")
	gate := access.New()
	if err := gate.Reload(db); err != nil {
		t.Fatal(err)
	}
	engine := New(Deps{
		Base:     apibase.Deps{DB: db, Secret: "test-secret", Gate: gate},
		Tree:     apitree.Deps{DB: db},
		Monitor:  apimonitor.Deps{DB: db},
		Playbook: apiplay.Deps{DB: db},
	})
	lin := loginName(t, engine, "林夏", "secret")
	xu := loginName(t, engine, "许衡", "secret")
	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	pay := mustNode(t, nodes, "支付")
	cmdb := model.CMDBModel{Name: "服务", Code: "heal-svc"}
	if err := db.Create(&cmdb).Error; err != nil {
		t.Fatal(err)
	}
	obj := model.CMDBObject{ModelID: cmdb.ID, Name: "自愈对象", TreeNodeID: order.ID}
	other := model.CMDBObject{ModelID: cmdb.ID, Name: "另一个对象", TreeNodeID: order.ID}
	if err := db.Create(&obj).Error; err != nil || db.Create(&other).Error != nil {
		t.Fatal(err)
	}
	pools := decodeJSON[[]namedID](t, getAuth(engine, "/api/monitor/pools", lin))
	pool := mustNamed(t, pools, "交易采集")
	groups := decodeJSON[[]namedID](t, getAuth(engine, "/api/monitor/send-groups", lin))
	group := mustNamed(t, groups, "交易发送")

	if rec := postJSON(engine, "/api/monitor/rules", alertRuleBody("草稿规则", pool.ID, group.ID, order.ID, obj.ID, &draft.ID, nil), lin); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "剧本还没发布") {
		t.Fatalf("草稿剧本 = %d %s", rec.Code, rec.Body.String())
	}
	missing := uint(99999)
	if rec := postJSON(engine, "/api/monitor/rules", alertRuleBody("缺失规则", pool.ID, group.ID, order.ID, obj.ID, &missing, nil), lin); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "没有这个剧本") {
		t.Fatalf("缺失剧本 = %d %s", rec.Code, rec.Body.String())
	}
	badTemplate := `{"note":"{{nope}}"}`
	if rec := postJSON(engine, "/api/monitor/rules", alertRuleBody("坏模板", pool.ID, group.ID, order.ID, obj.ID, &note.ID, &badTemplate), lin); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "不认识的变量") {
		t.Fatalf("坏模板 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/monitor/rules", alertRuleBody("越权绑定", pool.ID, group.ID, order.ID, obj.ID, &note.ID, nil), xu); rec.Code != http.StatusForbidden {
		t.Fatalf("许衡绑定 = %d %s", rec.Code, rec.Body.String())
	}

	template := `{"service":"order","note":"处理 {{rule_name}}","tree_node_id":1}`
	created := postJSON(engine, "/api/monitor/rules", alertRuleBody("订单自愈", pool.ID, group.ID, order.ID, obj.ID, &note.ID, &template), lin)
	if created.Code != http.StatusOK {
		t.Fatalf("绑定剧本 = %d %s", created.Code, created.Body.String())
	}
	ruleID := decodeJSON[struct {
		ID uint `json:"id"`
	}](t, created).ID
	kept := putJSON(engine, fmt.Sprintf("/api/monitor/rules/%d", ruleID), alertRuleBody("订单自愈", pool.ID, group.ID, order.ID, obj.ID, nil, nil), lin)
	if kept.Code != http.StatusOK {
		t.Fatalf("不带剧本字段的修改 = %d %s", kept.Code, kept.Body.String())
	}
	listed := decodeJSON[[]healRuleJSON](t, getAuth(engine, "/api/monitor/rules", lin))
	got := mustHealRule(t, listed, "订单自愈")
	if got.PlaybookID != note.ID || got.PlaybookName != "告警记一笔" {
		t.Fatalf("剧本绑定 = %+v", got)
	}

	var runsBefore int64
	if err := db.Model(&model.Run{}).Count(&runsBefore).Error; err != nil {
		t.Fatal(err)
	}
	plain := postJSON(engine, "/api/monitor/alerts/webhook", fmt.Sprintf(`{"fingerprint":"no-rule","nodeId":%d,"objectId":%d}`, order.ID, obj.ID), lin)
	if plain.Code != http.StatusOK || strings.Contains(plain.Body.String(), `"started"`) {
		t.Fatalf("不带规则 = %d %s", plain.Code, plain.Body.String())
	}
	unboundBody := alertRuleBody("只告警", pool.ID, group.ID, order.ID, obj.ID, nil, nil)
	unboundRec := postJSON(engine, "/api/monitor/rules", unboundBody, lin)
	if unboundRec.Code != http.StatusOK {
		t.Fatalf("未绑定规则 = %d %s", unboundRec.Code, unboundRec.Body.String())
	}
	unboundID := decodeJSON[struct {
		ID uint `json:"id"`
	}](t, unboundRec).ID
	unbound := postJSON(engine, "/api/monitor/alerts/webhook", hookBody("only-alert", order.ID, obj.ID, unboundID, ""), lin)
	if unbound.Code != http.StatusOK || !strings.Contains(unbound.Body.String(), `"started":false`) || strings.Contains(unbound.Body.String(), `"runId"`) {
		t.Fatalf("未绑定触发 = %d %s", unbound.Code, unbound.Body.String())
	}
	var runsAfter int64
	if err := db.Model(&model.Run{}).Count(&runsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if runsBefore != runsAfter {
		t.Fatalf("未绑定却建了 Run %d -> %d", runsBefore, runsAfter)
	}
	if rec := postJSON(engine, "/api/monitor/alerts/webhook", hookBody("off-node", pay.ID, 0, ruleID, ""), lin); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "规则不在这个节点上") {
		t.Fatalf("节点不符 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/monitor/alerts/webhook", hookBody("off-object", order.ID, other.ID, ruleID, ""), lin); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "规则不在这个对象上") {
		t.Fatalf("对象不符 = %d %s", rec.Code, rec.Body.String())
	}

	first := postJSON(engine, "/api/monitor/alerts/webhook", hookBody("disk-full", order.ID, obj.ID, ruleID, "磁盘满了"), lin)
	if first.Code != http.StatusOK {
		t.Fatalf("首次触发 = %d %s", first.Code, first.Body.String())
	}
	started := decodeJSON[healHookJSON](t, first)
	if !started.Started || started.RunID == 0 || started.RunStatus != "paused" || started.Owners[0].Name != "林夏" {
		t.Fatalf("首次结果 = %+v", started)
	}
	var run model.Run
	if err := db.First(&run, started.RunID).Error; err != nil {
		t.Fatal(err)
	}
	if run.ActiveIdem == nil || *run.ActiveIdem == "" {
		t.Fatal("未结束的 Run 没有占住幂等键")
	}
	clone := run
	clone.ID = 0
	if err := db.Create(&clone).Error; err == nil {
		t.Fatal("同一个未结束幂等键写进了第二条 Run")
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(run.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	if input["rule_name"] != "订单自愈" || input["fingerprint"] != "disk-full" || input["summary"] != "磁盘满了" || input["service"] != "order" || input["note"] != "处理 订单自愈" {
		t.Fatalf("入参 = %+v", input)
	}
	if uint(input["tree_node_id"].(float64)) != order.ID || uint(input["object_id"].(float64)) != obj.ID {
		t.Fatalf("节点对象被模板改掉 %+v", input)
	}
	again := decodeJSON[healHookJSON](t, postJSON(engine, "/api/monitor/alerts/webhook", hookBody("disk-full", order.ID, obj.ID, ruleID, "磁盘满了"), lin))
	if again.Started || again.RunID != started.RunID {
		t.Fatalf("重复指纹 = %+v", again)
	}
	rows := healRows(t, engine, lin, "fingerprint=disk-full")
	if len(rows) != 1 || rows[0].RunID != started.RunID || rows[0].RunStatus != "paused" || rows[0].OwnerName != "林夏" {
		t.Fatalf("自愈记录 = %+v", rows)
	}
	cancelled := postJSON(engine, fmt.Sprintf("/api/playbook/runs/%d/cancel", started.RunID), "", lin)
	if cancelled.Code != http.StatusOK {
		t.Fatalf("取消 = %d %s", cancelled.Code, cancelled.Body.String())
	}
	rows = healRows(t, engine, lin, "fingerprint=disk-full")
	if len(rows) != 1 || rows[0].RunStatus != "cancelled" || rows[0].Detail != "cancelled" {
		t.Fatalf("取消回写 = %+v", rows)
	}
	if err := db.First(&run, started.RunID).Error; err != nil {
		t.Fatal(err)
	}
	if run.ActiveIdem != nil {
		t.Fatal("终态后幂等槽没有放开")
	}
	third := decodeJSON[healHookJSON](t, postJSON(engine, "/api/monitor/alerts/webhook", hookBody("disk-full", order.ID, obj.ID, ruleID, "又满了"), lin))
	if !third.Started || third.RunID == started.RunID || third.RunStatus != "paused" {
		t.Fatalf("终态后再触发 = %+v", third)
	}
	rows = healRows(t, engine, lin, "fingerprint=disk-full")
	if len(rows) != 2 {
		t.Fatalf("同一指纹应有两条自愈记录 %+v", rows)
	}
	byObject := healRows(t, engine, lin, fmt.Sprintf("objectId=%d", obj.ID))
	seenHeal := 0
	for _, row := range byObject {
		if row.Action == "heal" && row.Fingerprint == "disk-full" {
			seenHeal++
		}
	}
	if seenHeal != 2 {
		t.Fatalf("按对象查自愈 = %+v", byObject)
	}
	if xuRows := healRows(t, engine, xu, "fingerprint=disk-full"); len(xuRows) != 0 {
		t.Fatalf("许衡看见了自愈记录 %+v", xuRows)
	}

	failRule := postJSON(engine, "/api/monitor/rules", alertRuleBody("会失败", pool.ID, group.ID, order.ID, obj.ID, &failBook.ID, nil), lin)
	if failRule.Code != http.StatusOK {
		t.Fatalf("失败剧本规则 = %d %s", failRule.Code, failRule.Body.String())
	}
	failID := decodeJSON[struct {
		ID uint `json:"id"`
	}](t, failRule).ID
	failed := decodeJSON[healHookJSON](t, postJSON(engine, "/api/monitor/alerts/webhook", hookBody("boom", order.ID, obj.ID, failID, ""), lin))
	if !failed.Started || failed.RunStatus != "failed" {
		t.Fatalf("失败触发 = %+v", failed)
	}
	failRows := healRows(t, engine, lin, "fingerprint=boom")
	if len(failRows) != 1 || failRows[0].RunStatus != "failed" || failRows[0].Detail != "不认识的步骤" {
		t.Fatalf("失败回写 = %+v", failRows)
	}

	cleared := putJSON(engine, fmt.Sprintf("/api/monitor/rules/%d", ruleID), alertRuleBody("订单自愈", pool.ID, group.ID, order.ID, obj.ID, uintPtr(0), nil), lin)
	if cleared.Code != http.StatusOK {
		t.Fatalf("解开绑定 = %d %s", cleared.Code, cleared.Body.String())
	}
	listed = decodeJSON[[]healRuleJSON](t, getAuth(engine, "/api/monitor/rules", lin))
	if mustHealRule(t, listed, "订单自愈").PlaybookID != 0 {
		t.Fatal("剧本没有解开")
	}
	var beforeClear int64
	if err := db.Model(&model.Run{}).Count(&beforeClear).Error; err != nil {
		t.Fatal(err)
	}
	quiet := postJSON(engine, "/api/monitor/alerts/webhook", hookBody("disk-full", order.ID, obj.ID, ruleID, ""), lin)
	if quiet.Code != http.StatusOK || strings.Contains(quiet.Body.String(), `"runId"`) {
		t.Fatalf("解开后触发 = %d %s", quiet.Code, quiet.Body.String())
	}
	var afterClear int64
	if err := db.Model(&model.Run{}).Count(&afterClear).Error; err != nil {
		t.Fatal(err)
	}
	if beforeClear != afterClear {
		t.Fatal("解开绑定后又建了 Run")
	}
	binds := 0
	for _, row := range healRows(t, engine, lin, fmt.Sprintf("objectId=%d", obj.ID)) {
		if row.Action == "bind" && row.RuleID == ruleID {
			binds++
		}
	}
	if binds < 2 {
		t.Fatalf("绑定记录不足 %d", binds)
	}
	if rec := postJSON(engine, "/api/monitor/alerts/assign", fmt.Sprintf(`{"fingerprint":"disk-full","nodeId":%d,"objectId":%d}`, order.ID, obj.ID), lin); rec.Code != http.StatusOK {
		t.Fatalf("认领 = %d %s", rec.Code, rec.Body.String())
	}
}

func TestWebhookOnlyStartsRun(t *testing.T) {
	raw, err := os.ReadFile("../api/monitor/handler.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, banned := range []string{
		"xuntai/internal/task",
		"xuntai/internal/cicd",
		"xuntai/internal/k8s",
		"xuntai/internal/db",
	} {
		if strings.Contains(src, banned) {
			t.Fatalf("告警处理引用了 %s", banned)
		}
	}
	if strings.Count(src, "playcore.Start") != 1 {
		t.Fatalf("剧本启动出现 %d 次", strings.Count(src, "playcore.Start"))
	}
}

func mustHealBook(t *testing.T, db *gorm.DB, code, name, status, kind string) model.Playbook {
	t.Helper()
	book := model.Playbook{Code: code, Name: name, Status: status, InputSchema: "{}"}
	if err := db.Create(&book).Error; err != nil {
		t.Fatal(err)
	}
	step := model.PlaybookStep{PlaybookID: book.ID, Seq: 1, StepKey: "only", Kind: kind, InputMapping: "{}", OnError: "stop"}
	if err := db.Create(&step).Error; err != nil {
		t.Fatal(err)
	}
	return book
}

func alertRuleBody(name string, pool, group, node, object uint, playbook *uint, template *string) string {
	payload := map[string]any{
		"name": name, "expr": "up == 0", "level": "警告",
		"poolId": pool, "sendGroupId": group, "treeNodeId": node, "objectId": object,
	}
	if playbook != nil {
		payload["playbookId"] = *playbook
	}
	if template != nil {
		payload["inputTemplate"] = *template
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func hookBody(fingerprint string, node, object, rule uint, summary string) string {
	payload := map[string]any{
		"fingerprint": fingerprint, "nodeId": node, "objectId": object, "summary": summary,
	}
	if rule != 0 {
		payload["ruleId"] = rule
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func uintPtr(v uint) *uint { return &v }

type healRuleJSON struct {
	Name         string `json:"name"`
	PlaybookID   uint   `json:"playbookId"`
	PlaybookName string `json:"playbookName"`
}

type healHookJSON struct {
	Started   bool   `json:"started"`
	RunID     uint   `json:"runId"`
	RunStatus string `json:"runStatus"`
	Owners    []struct {
		Name string `json:"name"`
	} `json:"owners"`
}

type healActionJSON struct {
	Action      string `json:"action"`
	Fingerprint string `json:"fingerprint"`
	RunID       uint   `json:"runId"`
	RuleID      uint   `json:"ruleId"`
	RunStatus   string `json:"runStatus"`
	Detail      string `json:"detail"`
	OwnerName   string `json:"ownerName"`
}

func mustHealRule(t *testing.T, rows []healRuleJSON, name string) healRuleJSON {
	t.Helper()
	for _, row := range rows {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("没有规则 %s", name)
	return healRuleJSON{}
}

func healRows(t *testing.T, engine http.Handler, token, query string) []healActionJSON {
	t.Helper()
	rec := getAuth(engine, "/api/monitor/alerts/actions?"+query, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("处理记录 = %d %s", rec.Code, rec.Body.String())
	}
	return decodeJSON[[]healActionJSON](t, rec)
}
