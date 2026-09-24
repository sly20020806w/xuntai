package access

import (
	"strings"
	"sync"

	"github.com/casbin/casbin/v2"
	casbinmodel "github.com/casbin/casbin/v2/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xuntai/internal/auth"
	"xuntai/internal/model"
)

const modelText = `
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = r.sub == p.sub && keyMatch2(r.obj, p.obj) && r.act == p.act
`

type Gate struct {
	mu sync.RWMutex
	e  *casbin.Enforcer
}

func New() *Gate {
	return &Gate{}
}

func (g *Gate) Reload(db *gorm.DB) error {
	m, err := casbinmodel.NewModelFromString(modelText)
	if err != nil {
		return err
	}
	enforcer, err := casbin.NewEnforcer(m)
	if err != nil {
		return err
	}
	var roles []model.Role
	if err := db.Preload("APIs").Find(&roles).Error; err != nil {
		return err
	}
	for _, role := range roles {
		for _, api := range role.APIs {
			if _, err := enforcer.AddPolicy(role.Name, api.Path, api.Method); err != nil {
				return err
			}
		}
	}
	g.mu.Lock()
	g.e = enforcer
	g.mu.Unlock()
	return nil
}

func (g *Gate) Allow(roles []string, path, method string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.e == nil {
		return false
	}
	for _, role := range roles {
		ok, err := g.e.Enforce(role, path, method)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func Middleware(secret string, gate *Gate) gin.HandlerFunc {
	return func(c *gin.Context) {
		if isPublic(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}
		claims, err := auth.Parse(secret, c.GetHeader("Authorization"))
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "需要登录"})
			return
		}
		c.Set("uid", claims.UID)
		c.Set("name", claims.Name)
		if gate == nil || !gate.Allow(claims.Roles, c.Request.URL.Path, c.Request.Method) {
			c.AbortWithStatusJSON(403, gin.H{"error": "没有权限"})
			return
		}
		c.Next()
	}
}

func isPublic(method, path string) bool {
	if path == "/healthz" || method == "OPTIONS" {
		return true
	}
	if method == "POST" && path == "/api/base/login" {
		return true
	}
	return strings.HasPrefix(path, "/api/") && strings.HasSuffix(path, "/ping")
}
