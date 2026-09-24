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
	apitask "xuntai/internal/api/task"
	apiticket "xuntai/internal/api/ticket"
	apitree "xuntai/internal/api/tree"
	"xuntai/internal/base"
	"xuntai/internal/model"
	"xuntai/internal/task"
	"xuntai/internal/ticket"
	"xuntai/internal/tree"
)

func TestTaskBatchAndUniqueHost(t *testing.T) {
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
	gate := access.New()
	if err := gate.Reload(db); err != nil {
		t.Fatal(err)
	}
	engine := New(Deps{
		Base:   apibase.Deps{DB: db, Secret: "test-secret", Gate: gate},
		Tree:   apitree.Deps{DB: db},
		Ticket: apiticket.Deps{DB: db},
		Task:   apitask.Deps{DB: db},
	})

	zhou := loginName(t, engine, "周宁", "secret")
	lin := loginName(t, engine, "林夏", "secret")
	xu := loginName(t, engine, "许衡", "secret")
	chen := loginName(t, engine, "陈舟", "secret")

	nodes := decodeNodes(t, getAuth(engine, "/api/tree/nodes", lin))
	order := mustNode(t, nodes, "订单")
	scripts := decodeJSON[[]struct {
		ID   uint   `json:"ID"`
		Name string `json:"Name"`
	}](t, getAuth(engine, "/api/task/scripts", lin))
	var scriptID uint
	for _, script := range scripts {
		if script.Name == "核对时钟" {
			scriptID = script.ID
		}
	}
	if scriptID == 0 {
		t.Fatal("没有核对时钟脚本")
	}

	body := fmt.Sprintf(`{"name":"订单对时","scriptId":%d,"treeNodeId":%d,"batchSize":1}`, scriptID, order.ID)
	if rec := postJSON(engine, "/api/task/jobs", body, chen); rec.Code != http.StatusForbidden {
		t.Fatalf("陈舟下发 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(engine, "/api/task/jobs", body, zhou); rec.Code != http.StatusForbidden {
		t.Fatalf("周宁下发订单 = %d %s", rec.Code, rec.Body.String())
	}
	foreign := fmt.Sprintf(`{"name":"串节点","scriptId":%d,"treeNodeId":%d,"batchSize":1,"hosts":["10.4.1.21"]}`, scriptID, order.ID)
	if rec := postJSON(engine, "/api/task/jobs", foreign, lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("跨节点机器 = %d %s", rec.Code, rec.Body.String())
	}
	dup := fmt.Sprintf(`{"name":"重复","scriptId":%d,"treeNodeId":%d,"batchSize":1,"hosts":["10.8.2.17","10.8.2.17"]}`, scriptID, order.ID)
	if rec := postJSON(engine, "/api/task/jobs", dup, lin); rec.Code != http.StatusBadRequest {
		t.Fatalf("重复机器 = %d %s", rec.Code, rec.Body.String())
	}

	created := postJSON(engine, "/api/task/jobs", body, lin)
	if created.Code != http.StatusOK {
		t.Fatalf("林夏下发 = %d %s", created.Code, created.Body.String())
	}
	var fresh struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &fresh); err != nil {
		t.Fatal(err)
	}
	job := mustJob(t, decodeJSON[[]jobJSON](t, getAuth(engine, "/api/task/jobs", lin)), "订单对时")
	if job.Total != 2 || job.Done != 0 || len(job.Issued) != 1 {
		t.Fatalf("首轮进度 = %+v", job)
	}
	if _, ok := findJob(decodeJSON[[]jobJSON](t, getAuth(engine, "/api/task/jobs", xu)), "内核参数基线"); ok {
		t.Fatal("许衡看见了订单任务")
	}

	report := func(host, status string) *httptest.ResponseRecorder {
		return postJSON(engine, fmt.Sprintf("/api/task/jobs/%d/results", fresh.ID), fmt.Sprintf(`{"hostIp":"%s","status":"%s","output":"ok"}`, host, status), lin)
	}
	if rec := report(job.Issued[0], "success"); rec.Code != http.StatusOK {
		t.Fatalf("收回第一台 = %d %s", rec.Code, rec.Body.String())
	}
	job = mustJob(t, decodeJSON[[]jobJSON](t, getAuth(engine, "/api/task/jobs", lin)), "订单对时")
	if job.Done != 1 || job.Status != "running" || len(job.Issued) != 1 {
		t.Fatalf("第二轮进度 = %+v", job)
	}
	if rec := report("10.9.9.9", "success"); rec.Code != http.StatusNotFound {
		t.Fatalf("未知机器 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := report(job.Issued[0], "success"); rec.Code != http.StatusOK {
		t.Fatalf("收回第二台 = %d %s", rec.Code, rec.Body.String())
	}
	job = mustJob(t, decodeJSON[[]jobJSON](t, getAuth(engine, "/api/task/jobs", lin)), "订单对时")
	if job.Status != "finished" || job.Done != 2 || job.Total != 2 {
		t.Fatalf("完成后 = %+v", job)
	}
	var results int64
	if err := db.Model(&model.JobResult{}).Where("job_id = ?", fresh.ID).Count(&results).Error; err != nil {
		t.Fatal(err)
	}
	if results != 2 {
		t.Fatalf("结果行数 = %d", results)
	}

	again := postJSON(engine, "/api/task/jobs", fmt.Sprintf(`{"name":"订单再对时","scriptId":%d,"treeNodeId":%d,"batchSize":1}`, scriptID, order.ID), lin)
	if again.Code != http.StatusOK {
		t.Fatalf("第二张任务 = %d %s", again.Code, again.Body.String())
	}
	var second struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(again.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if rec := postJSON(engine, fmt.Sprintf("/api/task/jobs/%d/pause", second.ID), "", lin); rec.Code != http.StatusOK {
		t.Fatalf("暂停 = %d %s", rec.Code, rec.Body.String())
	}
	paused := mustJob(t, decodeJSON[[]jobJSON](t, getAuth(engine, "/api/task/jobs", lin)), "订单再对时")
	if rec := postJSON(engine, fmt.Sprintf("/api/task/jobs/%d/results", second.ID), fmt.Sprintf(`{"hostIp":"%s","status":"success","output":"ok"}`, paused.Issued[0]), lin); rec.Code != http.StatusOK {
		t.Fatalf("暂停后收回 = %d %s", rec.Code, rec.Body.String())
	}
	paused = mustJob(t, decodeJSON[[]jobJSON](t, getAuth(engine, "/api/task/jobs", lin)), "订单再对时")
	if paused.Status != "paused" || paused.Done != 1 || len(paused.Issued) != 0 {
		t.Fatalf("暂停后不应放出下一台 = %+v", paused)
	}
}

type jobJSON struct {
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Done     int      `json:"done"`
	Total    int      `json:"total"`
	Issued   []string `json:"issued"`
	NodeName string   `json:"nodeName"`
}

func mustJob(t *testing.T, jobs []jobJSON, name string) jobJSON {
	t.Helper()
	job, ok := findJob(jobs, name)
	if !ok {
		t.Fatalf("没有任务 %s", name)
	}
	return job
}

func findJob(jobs []jobJSON, name string) (jobJSON, bool) {
	for _, job := range jobs {
		if job.Name == name {
			return job, true
		}
	}
	return jobJSON{}, false
}
