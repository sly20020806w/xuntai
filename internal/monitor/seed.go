package monitor

import (
	"xuntai/internal/base"
	"xuntai/internal/model"

	"gorm.io/gorm"
)

func APICatalog() []model.API {
	return []model.API{
		{Method: "GET", Path: "/api/monitor/pools"},
		{Method: "POST", Path: "/api/monitor/pools"},
		{Method: "GET", Path: "/api/monitor/pools/:id/targets"},
		{Method: "GET", Path: "/api/monitor/jobs"},
		{Method: "POST", Path: "/api/monitor/jobs"},
		{Method: "GET", Path: "/api/monitor/send-groups"},
		{Method: "GET", Path: "/api/monitor/rules"},
		{Method: "POST", Path: "/api/monitor/rules"},
	}
}

// Seed 补上监控接口。还没有采集池时放入池、叶子发现、发送组和规则。
func Seed(db *gorm.DB) (bool, error) {
	for _, role := range []string{"平台管理员", "节点运维"} {
		if err := base.Grant(db, role, APICatalog()); err != nil {
			return false, err
		}
	}
	var count int64
	if err := db.Model(&model.ScrapePool{}).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		basePool := model.ScrapePool{Name: "基础采集", RemoteWrite: "远端存储", SupportAlert: true}
		tradePool := model.ScrapePool{Name: "交易采集", RemoteWrite: "远端存储", SupportAlert: true}
		if err := tx.Create(&basePool).Error; err != nil {
			return err
		}
		if err := tx.Create(&tradePool).Error; err != nil {
			return err
		}
		observe, err := nodeID(tx, "可观测")
		if err != nil {
			return err
		}
		order, err := nodeID(tx, "订单")
		if err != nil {
			return err
		}
		pay, err := nodeID(tx, "支付")
		if err != nil {
			return err
		}
		jobs := []model.ScrapeJob{
			{PoolID: basePool.ID, TreeNodeID: observe, Name: "node-exporter", Discover: "tree", Port: 9100, MetricsPath: "/metrics"},
			{PoolID: tradePool.ID, TreeNodeID: order, Name: "订单进程", Discover: "tree", Port: 9256, MetricsPath: "/metrics"},
			{PoolID: tradePool.ID, TreeNodeID: pay, Name: "支付进程", Discover: "tree", Port: 9256, MetricsPath: "/metrics"},
		}
		if err := tx.Create(&jobs).Error; err != nil {
			return err
		}
		cluster := model.AlertmanagerCluster{Name: "主集群", Endpoints: "http://am-a:9093,http://am-b:9093"}
		if err := tx.Create(&cluster).Error; err != nil {
			return err
		}
		duties := []model.DutyGroup{
			{Name: "交易值班", ShiftDays: 1},
			{Name: "基础架构值班", ShiftDays: 1},
			{Name: "可观测值班", ShiftDays: 1},
		}
		if err := tx.Create(&duties).Error; err != nil {
			return err
		}
		dutyID := map[string]uint{}
		for _, duty := range duties {
			dutyID[duty.Name] = duty.ID
		}
		groups := []model.SendGroup{
			{Name: "交易发送", DutyGroupID: dutyID["交易值班"], ClusterID: cluster.ID},
			{Name: "基础架构发送", DutyGroupID: dutyID["基础架构值班"], ClusterID: cluster.ID},
			{Name: "可观测发送", DutyGroupID: dutyID["可观测值班"], ClusterID: cluster.ID},
		}
		if err := tx.Create(&groups).Error; err != nil {
			return err
		}
		groupID := map[string]uint{}
		for _, group := range groups {
			groupID[group.Name] = group.ID
		}
		rules := []model.AlertRule{
			{Name: "订单错误率", Expr: "sum(rate(http_requests_total{status=~\"5..\"}[5m])) > 1", Level: "紧急", PoolID: tradePool.ID, SendGroupID: groupID["交易发送"]},
			{Name: "入口 5xx", Expr: "sum(rate(http_requests_total{job=\"edge\"}[5m])) > 1", Level: "警告", PoolID: basePool.ID, SendGroupID: groupID["基础架构发送"]},
			{Name: "采集目标失联", Expr: "up == 0", Level: "警告", PoolID: basePool.ID, SendGroupID: groupID["可观测发送"]},
		}
		if err := tx.Create(&rules).Error; err != nil {
			return err
		}
		ruleID := map[string]uint{}
		for _, rule := range rules {
			ruleID[rule.Name] = rule.ID
		}
		events := []model.AlertEvent{
			{RuleID: ruleID["订单错误率"], Fingerprint: "order-5xx", Status: "firing"},
			{RuleID: ruleID["入口 5xx"], Fingerprint: "edge-5xx", Status: "claimed"},
			{RuleID: ruleID["采集目标失联"], Fingerprint: "up-down", Status: "silenced"},
		}
		return tx.Create(&events).Error
	})
	return err == nil, err
}

func nodeID(tx *gorm.DB, name string) (uint, error) {
	var node model.Node
	if err := tx.Where("name = ?", name).First(&node).Error; err != nil {
		return 0, err
	}
	return node.ID, nil
}
