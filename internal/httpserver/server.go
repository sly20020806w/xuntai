package httpserver

import (
	"github.com/gin-gonic/gin"

	"xuntai/internal/api/base"
	"xuntai/internal/api/cicd"
	"xuntai/internal/api/db"
	"xuntai/internal/api/k8s"
	"xuntai/internal/api/monitor"
	"xuntai/internal/api/task"
	"xuntai/internal/api/ticket"
	"xuntai/internal/api/tree"
)

func New() *gin.Engine {
	r := gin.New()
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	base.Register(r.Group("/api/base"))
	tree.Register(r.Group("/api/tree"))
	ticket.Register(r.Group("/api/ticket"))
	task.Register(r.Group("/api/task"))
	monitor.Register(r.Group("/api/monitor"))
	k8s.Register(r.Group("/api/k8s"))
	cicd.Register(r.Group("/api/cicd"))
	db.Register(r.Group("/api/db"))
	return r
}

func Run(addr string) error {
	return New().Run(addr)
}
