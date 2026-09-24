package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHealthz(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("响应 = %s", rec.Body.String())
	}
}

func TestModuleRoutesAreMounted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := New()
	routes := engine.Routes()
	found := map[string]bool{}
	for _, route := range routes {
		found[route.Path] = true
	}
	if !found["/healthz"] {
		t.Fatal("缺少 /healthz")
	}
	for _, path := range []string{
		"/api/base/ping",
		"/api/tree/ping",
		"/api/ticket/ping",
		"/api/task/ping",
		"/api/monitor/ping",
		"/api/k8s/ping",
		"/api/cicd/ping",
		"/api/db/ping",
	} {
		if !found[path] {
			t.Fatalf("缺少路由 %s", path)
		}
	}
}
