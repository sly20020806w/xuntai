package model

import (
	"slices"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestTicketStatuses(t *testing.T) {
	want := []string{"pending_approve", "reject", "pending_action", "finished"}
	if !slices.Equal(TicketStatuses, want) {
		t.Fatalf("工单状态 = %v", TicketStatuses)
	}
}

func TestMigrateCreatesModuleTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}

	var names []string
	err = db.Raw("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name").Scan(&names).Error
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"base_apis",
		"base_menus",
		"base_role_apis",
		"base_role_menus",
		"base_roles",
		"base_user_roles",
		"base_users",
		"cicd_deploy_items",
		"cicd_orders",
		"cicd_stages",
		"db_backups",
		"db_instances",
		"db_restores",
		"k8s_apps",
		"k8s_clusters",
		"k8s_instances",
		"k8s_projects",
		"model",
		"monitor_alert_events",
		"monitor_alert_rules",
		"monitor_alertmanager_clusters",
		"monitor_duty_groups",
		"monitor_scrape_jobs",
		"monitor_scrape_pools",
		"monitor_send_groups",
		"object",
		"object_node",
		"playbook",
		"playbook_step",
		"run",
		"run_step",
		"task_jobs",
		"task_results",
		"task_scripts",
		"ticket_instances",
		"ticket_templates",
		"tree_machines",
		"tree_node_machines",
		"tree_node_owners",
		"tree_nodes",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("表 = %v\n期望 %v", names, want)
	}
}
