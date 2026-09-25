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
	apiticket "xuntai/internal/api/ticket"
	apitree "xuntai/internal/api/tree"
	"xuntai/internal/base"
	dbcore "xuntai/internal/db"
	"xuntai/internal/model"
	"xuntai/internal/ticket"
	"xuntai/internal/tree"
)

func TestDbRegistryAndRestoreTicket(t *testing.T) {
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
		func(db *gorm.DB) error { _, err := dbcore.Seed(db); return err },
	} {
		if err := seed(db); err != nil {
			t.Fatal(err)
		}
	}
	var cols []string
	if err := db.Raw("SELECT name FROM pragma_table_info('db_instances')").Scan(&cols).Error; err != nil {
		t.Fatal(err)
	}
	for _, col := range cols {
		if strings.Contains(strings.ToLower(col), "password") || strings.Contains(strings.ToLower(col), "lag") {
			t.Fatalf("实例表不该有 %s", col)
		}
	}
	gate := access.New()
	if err := gate.Reload(db); err != nil {
		t.Fatal(err)
	}
	engine := New(Deps{
		Base:   apibase.Deps{DB: db, Secret: "test-secret", Gate: gate},
		Tree:   apitree.Deps{DB: db},
		Ticket: apiticket.Deps{DB: db},
		Db:     apidb.Deps{DB: db},
	})
	for _, route := range engine.Routes() {
		if strings.Contains(route.Path, "xtrabackup") || strings.Contains(route.Path, "/exec") {
			t.Fatalf("不该有执行备份的路由 %s", route.Path)
		}
	}

	zhou := loginName(t, engine, "周宁", "secret")
	lin := loginName(t, engine, "林夏", "secret")
	xu := loginName(t, engine, "许衡", "secret")
	chen := loginName(t, engine, "陈舟", "secret")

	if hidden := getAuth(engine, "/api/db/instances", xu); hidden.Code != http.StatusOK || strings.Contains(hidden.Body.String(), "订单主库") {
		t.Fatalf("许衡的数据库列表 = %d %s", hidden.Code, hidden.Body.String())
	}
	listed := getAuth(engine, "/api/db/instances", lin)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), "password") || strings.Contains(listed.Body.String(), "1234") {
		t.Fatalf("实例列表 = %d %s", listed.Code, listed.Body.String())
	}
	instances := decodeJSON[[]dbInstanceJSON](t, listed)
	master := mustDbInstance(t, instances, "订单主库")
	if master.Role != "主" || master.Running || master.Host != "10.8.2.17" || master.ObjectID == 0 || master.Env != "生产" {
		t.Fatalf("主库登记不对 %+v", master)
	}
	detail := getAuth(engine, fmt.Sprintf("/api/db/instances/%d", master.ID), lin)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "订单主库") || strings.Contains(detail.Body.String(), "password") {
		t.Fatalf("实例详情 = %d %s", detail.Code, detail.Body.String())
	}
	if hiddenDetail := getAuth(engine, fmt.Sprintf("/api/db/instances/%d", master.ID), xu); hiddenDetail.Code != http.StatusNotFound {
		t.Fatalf("许衡看实例 = %d %s", hiddenDetail.Code, hiddenDetail.Body.String())
	}
	slave := mustDbInstance(t, instances, "订单从库")
	if slave.MasterID != master.ID || slave.MasterName != "订单主库" || slave.Running {
		t.Fatalf("从库关系不对 %+v", slave)
	}
	if _, ok := findDbInstance(instances, "支付主库"); !ok {
		t.Fatal("林夏看不见支付主库")
	}

	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	orderNode := mustNode(t, nodes, "订单")
	trade := mustNode(t, nodes, "交易")
	if rec := postJSON(engine, "/api/db/instances", fmt.Sprintf(`{"name":"越权库","treeNodeId":%d,"host":"10.8.2.17","port":3307,"version":"8.0","role":"从","masterId":%d}`, orderNode.ID, master.ID), xu); rec.Code != http.StatusForbidden {
		t.Fatalf("许衡登记实例 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/db/instances", `{"name":"悬空库","host":"10.8.2.17","version":"8.0","role":"主"}`, lin); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "资源未挂载到服务树，无法校验归属") {
		t.Fatalf("未挂树实例 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/db/instances", fmt.Sprintf(`{"name":"父节点库","treeNodeId":%d,"host":"10.8.2.17","version":"8.0","role":"主"}`, trade.ID), lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("父节点实例 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/db/instances", fmt.Sprintf(`{"name":"第二主库","treeNodeId":%d,"host":"10.8.2.18","port":3307,"version":"8.0","role":"主"}`, orderNode.ID), lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("第二主库 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/db/instances", fmt.Sprintf(`{"name":"外来库","treeNodeId":%d,"host":"10.9.9.9","version":"8.0","role":"主"}`, orderNode.ID), lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("外来地址 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/db/instances", fmt.Sprintf(`{"name":"带口令","treeNodeId":%d,"host":"10.8.2.17","port":3307,"version":"8.0","role":"从","masterId":%d,"password":"1234"}`, orderNode.ID, master.ID), lin); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "不保存数据库口令") {
		t.Fatalf("口令 = %d %s", rec.Code, rec.Body.String())
	}

	pay := mustDbInstance(t, instances, "支付主库")
	if rec := postJSON(engine, "/api/db/backups", fmt.Sprintf(`{"instanceId":%d,"kind":"增量"}`, pay.ID), lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("没有全量的增量 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/db/backups", fmt.Sprintf(`{"instanceId":%d,"kind":"全量","tool":"mysqldump"}`, pay.ID), lin); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "物理备份") {
		t.Fatalf("逻辑备份 = %d %s", rec.Code, rec.Body.String())
	}
	full := postJSON(engine, "/api/db/backups", fmt.Sprintf(`{"instanceId":%d,"kind":"全量"}`, pay.ID), lin)
	if full.Code != http.StatusOK || !strings.Contains(full.Body.String(), `"running":false`) {
		t.Fatalf("全量备份 = %d %s", full.Code, full.Body.String())
	}
	var fullID struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(full.Body.Bytes(), &fullID); err != nil || fullID.ID == 0 {
		t.Fatal(err)
	}
	if rec := postJSON(engine, "/api/db/backups", fmt.Sprintf(`{"instanceId":%d,"kind":"增量","keep":3}`, pay.ID), lin); rec.Code != http.StatusOK {
		t.Fatalf("增量备份 = %d %s", rec.Code, rec.Body.String())
	}

	orderBackup := decodeJSON[[]struct {
		ID           uint   `json:"id"`
		InstanceName string `json:"instanceName"`
		Kind         string `json:"kind"`
		Running      bool   `json:"running"`
	}](t, getAuth(engine, "/api/db/backups", lin))
	var backupID uint
	for _, row := range orderBackup {
		if row.InstanceName == "订单主库" && row.Kind == "全量" {
			backupID = row.ID
			if row.Running {
				t.Fatal("备份被标成已经执行")
			}
		}
	}
	if backupID == 0 {
		t.Fatal("没有订单主库的全量备份")
	}
	if rec := postJSON(engine, "/api/db/restores", fmt.Sprintf(`{"backupId":%d,"instanceId":%d}`, backupID, slave.ID), lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("无工单还原 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/db/restores", fmt.Sprintf(`{"backupId":%d,"instanceId":%d,"ticketId":1}`, backupID, pay.ID), lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("跨节点还原 = %d %s", rec.Code, rec.Body.String())
	}

	templates := decodeJSON[[]namedID](t, getAuth(engine, "/api/ticket/templates", chen))
	created := postJSON(engine, "/api/ticket/instances", fmt.Sprintf(`{"templateId":%d,"treeNodeId":%d,"title":"订单从库还原"}`, templates[0].ID, orderNode.ID), chen)
	if created.Code != http.StatusOK {
		t.Fatalf("提单 = %d %s", created.Code, created.Body.String())
	}
	var ticketID struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &ticketID); err != nil || ticketID.ID == 0 {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"backupId":%d,"instanceId":%d,"ticketId":%d}`, backupID, slave.ID, ticketID.ID)
	if rec := postJSON(engine, "/api/db/restores", body, lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("未审批还原 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/ticket/instances/%d/approve", ticketID.ID), "", lin); rec.Code != http.StatusOK {
		t.Fatalf("审批 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/db/restores", body, xu); rec.Code != http.StatusForbidden {
		t.Fatalf("许衡还原 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/db/restores", body, zhou); rec.Code != http.StatusForbidden {
		t.Fatalf("周宁还原 = %d %s", rec.Code, rec.Body.String())
	}
	restored := postJSON(engine, "/api/db/restores", body, lin)
	if restored.Code != http.StatusOK || !strings.Contains(restored.Body.String(), "已登记") || !strings.Contains(restored.Body.String(), `"running":false`) {
		t.Fatalf("审批后还原 = %d %s", restored.Code, restored.Body.String())
	}
	tickets := decodeJSON[[]struct {
		ID     uint   `json:"id"`
		Status string `json:"status"`
	}](t, getAuth(engine, "/api/ticket/instances", lin))
	for _, row := range tickets {
		if row.ID == ticketID.ID && row.Status != "pending_action" {
			t.Fatalf("还原不该把工单做完，状态 = %s", row.Status)
		}
	}
}

type dbInstanceJSON struct {
	ID         uint   `json:"id"`
	Name       string `json:"name"`
	Host       string `json:"host"`
	Role       string `json:"role"`
	MasterID   uint   `json:"masterId"`
	MasterName string `json:"masterName"`
	ObjectID   uint   `json:"objectId"`
	Env        string `json:"env"`
	Running    bool   `json:"running"`
}

func mustDbInstance(t *testing.T, rows []dbInstanceJSON, name string) dbInstanceJSON {
	t.Helper()
	row, ok := findDbInstance(rows, name)
	if !ok {
		t.Fatalf("没有实例 %s", name)
	}
	return row
}

func findDbInstance(rows []dbInstanceJSON, name string) (dbInstanceJSON, bool) {
	for _, row := range rows {
		if row.Name == name {
			return row, true
		}
	}
	return dbInstanceJSON{}, false
}
