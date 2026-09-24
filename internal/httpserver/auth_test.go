package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"xuntai/internal/access"
	apibase "xuntai/internal/api/base"
	"xuntai/internal/auth"
	"xuntai/internal/base"
	"xuntai/internal/model"
)

func TestLoginMenusAndRoleBoundary(t *testing.T) {
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
	gate := access.New()
	if err := gate.Reload(db); err != nil {
		t.Fatal(err)
	}
	engine := New(Deps{Base: apibase.Deps{DB: db, Secret: "test-secret", Gate: gate}})

	denied := postJSON(engine, "/api/base/login", `{"name":"周宁","password":"nope"}`, "")
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("错误密码状态码 = %d", denied.Code)
	}

	login := postJSON(engine, "/api/base/login", `{"name":"周宁","password":"secret"}`, "")
	if login.Code != http.StatusOK {
		t.Fatalf("登录状态码 = %d %s", login.Code, login.Body.String())
	}
	var session struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	me := getAuth(engine, "/api/base/me", session.Token)
	if me.Code != http.StatusOK {
		t.Fatalf("本人信息状态码 = %d %s", me.Code, me.Body.String())
	}
	var profile struct {
		Menus []struct {
			Name string `json:"name"`
		} `json:"menus"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if len(profile.Menus) != 8 {
		t.Fatalf("菜单数量 = %d", len(profile.Menus))
	}

	users := getAuth(engine, "/api/base/users", session.Token)
	if users.Code != http.StatusOK {
		t.Fatalf("用户列表状态码 = %d", users.Code)
	}

	created := postJSON(engine, "/api/base/users", `{"name":"林夏","password":"secret"}`, session.Token)
	if created.Code != http.StatusOK {
		t.Fatalf("创建用户状态码 = %d %s", created.Code, created.Body.String())
	}
	other := postJSON(engine, "/api/base/login", `{"name":"林夏","password":"secret"}`, "")
	var otherSession struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(other.Body.Bytes(), &otherSession); err != nil {
		t.Fatal(err)
	}
	forbidden := getAuth(engine, "/api/base/users", otherSession.Token)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("无角色访问用户列表状态码 = %d", forbidden.Code)
	}
}

func TestTokenRoleClaimDoesNotGrantAccess(t *testing.T) {
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
	gate := access.New()
	if err := gate.Reload(db); err != nil {
		t.Fatal(err)
	}
	engine := New(Deps{Base: apibase.Deps{DB: db, Secret: "test-secret", Gate: gate}})

	plain := postJSON(engine, "/api/base/users", `{"name":"林夏","password":"secret"}`, loginToken(t, engine))
	if plain.Code != http.StatusOK {
		t.Fatalf("创建用户 = %d %s", plain.Code, plain.Body.String())
	}
	var created struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(plain.Body.Bytes(), &created); err != nil || created.ID == 0 {
		t.Fatal(err)
	}
	forged, err := auth.Sign("test-secret", created.ID, "林夏", []string{"平台管理员"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := getAuth(engine, "/api/base/users", forged); rec.Code != http.StatusForbidden {
		t.Fatalf("伪造角色仍能访问 = %d %s", rec.Code, rec.Body.String())
	}

	var zhou model.User
	if err := db.Where("name = ?", "周宁").First(&zhou).Error; err != nil {
		t.Fatal(err)
	}
	login := postJSON(engine, "/api/base/login", `{"name":"周宁","password":"secret"}`, "")
	var session struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&model.User{}, zhou.ID).Error; err != nil {
		t.Fatal(err)
	}
	if rec := getAuth(engine, "/api/base/users", session.Token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("已删除用户仍能访问 = %d %s", rec.Code, rec.Body.String())
	}
}

func loginToken(t *testing.T, engine http.Handler) string {
	t.Helper()
	login := postJSON(engine, "/api/base/login", `{"name":"周宁","password":"secret"}`, "")
	var session struct {
		Token string `json:"token"`
	}
	if login.Code != http.StatusOK || json.Unmarshal(login.Body.Bytes(), &session) != nil || session.Token == "" {
		t.Fatalf("登录失败 %d %s", login.Code, login.Body.String())
	}
	return session.Token
}

func postJSON(engine http.Handler, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func getAuth(engine http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}
