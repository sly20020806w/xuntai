package httpserver

import (
	"github.com/gin-gonic/gin"

	"xuntai/internal/access"
	apibase "xuntai/internal/api/base"
	"xuntai/internal/api/cicd"
	"xuntai/internal/api/db"
	"xuntai/internal/api/k8s"
	"xuntai/internal/api/monitor"
	"xuntai/internal/api/task"
	"xuntai/internal/api/ticket"
	"xuntai/internal/api/tree"
)

type Deps struct {
	Base    apibase.Deps
	Tree    tree.Deps
	Ticket  ticket.Deps
	Task    task.Deps
	Monitor monitor.Deps
	K8s     k8s.Deps
}

func New(deps Deps) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "http://127.0.0.1:5173")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})
	r.Use(access.Middleware(deps.Base.Secret, deps.Base.Gate))
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	apibase.Register(r.Group("/api/base"), deps.Base)
	tree.Register(r.Group("/api/tree"), deps.Tree)
	ticket.Register(r.Group("/api/ticket"), deps.Ticket)
	task.Register(r.Group("/api/task"), deps.Task)
	monitor.Register(r.Group("/api/monitor"), deps.Monitor)
	k8s.Register(r.Group("/api/k8s"), deps.K8s)
	cicd.Register(r.Group("/api/cicd"))
	db.Register(r.Group("/api/db"))
	return r
}

func Run(addr string, deps Deps) error {
	return New(deps).Run(addr)
}
