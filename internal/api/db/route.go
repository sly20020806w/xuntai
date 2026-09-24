// Package db 只保留模块位置。备份、主从和 Kubernetes 上的 MySQL 还没有定稿，这里不建表。
package db

import "github.com/gin-gonic/gin"

func Register(r *gin.RouterGroup) {
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"module": "db", "ready": false})
	})
}
