package main

import (
	"log"

	"xuntai/internal/access"
	apibase "xuntai/internal/api/base"
	apicicd "xuntai/internal/api/cicd"
	apidb "xuntai/internal/api/db"
	apik8s "xuntai/internal/api/k8s"
	apimonitor "xuntai/internal/api/monitor"
	apitask "xuntai/internal/api/task"
	apiticket "xuntai/internal/api/ticket"
	apitree "xuntai/internal/api/tree"
	"xuntai/internal/base"
	"xuntai/internal/cicd"
	"xuntai/internal/config"
	dbmod "xuntai/internal/db"
	"xuntai/internal/httpserver"
	"xuntai/internal/k8s"
	"xuntai/internal/model"
	"xuntai/internal/monitor"
	"xuntai/internal/scope"
	"xuntai/internal/store"
	"xuntai/internal/task"
	"xuntai/internal/ticket"
	"xuntai/internal/tree"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	scope.SetEnabled(cfg.ScopeFilter)
	logDB(cfg)
	db, err := store.Open(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err := model.Migrate(db); err != nil {
		log.Fatal(err)
	}
	created, err := base.Seed(db, cfg.AdminPassword)
	if err != nil {
		log.Fatal(err)
	}
	if created {
		log.Printf("已创建初始用户 周宁")
	}
	treeReady, err := tree.Seed(db, cfg.AdminPassword)
	if err != nil {
		log.Fatal(err)
	}
	if treeReady {
		log.Printf("已补上服务树示例，林夏、许衡、陈舟的密码与初始用户相同")
	}
	ticketReady, err := ticket.Seed(db)
	if err != nil {
		log.Fatal(err)
	}
	if ticketReady {
		log.Printf("已补上工单示例")
	}
	taskReady, err := task.Seed(db)
	if err != nil {
		log.Fatal(err)
	}
	if taskReady {
		log.Printf("已补上任务示例")
	}
	monitorReady, err := monitor.Seed(db)
	if err != nil {
		log.Fatal(err)
	}
	if monitorReady {
		log.Printf("已补上监控示例")
	}
	k8sReady, err := k8s.Seed(db)
	if err != nil {
		log.Fatal(err)
	}
	if k8sReady {
		log.Printf("已补上集群示例")
	}
	cicdReady, err := cicd.Seed(db)
	if err != nil {
		log.Fatal(err)
	}
	if cicdReady {
		log.Printf("已补上发布示例")
	}
	dbReady, err := dbmod.Seed(db)
	if err != nil {
		log.Fatal(err)
	}
	if dbReady {
		log.Printf("已补上数据库示例")
	}
	if err := base.ApplyPassword(db, cfg.AdminPassword); err != nil {
		log.Fatal(err)
	}
	gate := access.New()
	if err := gate.Reload(db); err != nil {
		log.Fatal(err)
	}
	if err := httpserver.Run(cfg.HTTPAddr, httpserver.Deps{
		Base:    apibase.Deps{DB: db, Secret: cfg.JWTSecret, Gate: gate},
		Tree:    apitree.Deps{DB: db},
		Ticket:  apiticket.Deps{DB: db},
		Task:    apitask.Deps{DB: db},
		Monitor: apimonitor.Deps{DB: db},
		K8s:     apik8s.Deps{DB: db},
		Cicd:    apicicd.Deps{DB: db},
		Db:      apidb.Deps{DB: db},
	}); err != nil {
		log.Fatal(err)
	}
}

func logDB(cfg config.Config) {
	if cfg.MySQLDSN != "" {
		log.Printf("使用 MySQL")
		return
	}
	log.Printf("未配置 MySQL，使用本地库 %s", cfg.SQLitePath)
}
