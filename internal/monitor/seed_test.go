package monitor

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"xuntai/internal/model"
)

func TestBindRuleNodesFillsNull(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := model.Migrate(db); err != nil {
		t.Fatal(err)
	}
	node := model.Node{Name: "订单", IsLeaf: true, Level: 3}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	rule := model.AlertRule{Name: "订单错误率", Expr: "up == 0", Level: "警告"}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE monitor_alert_rules SET tree_node_id = NULL WHERE id = ?", rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := bindRuleNodes(db); err != nil {
		t.Fatal(err)
	}
	var got model.AlertRule
	if err := db.First(&got, rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.TreeNodeID != node.ID {
		t.Fatalf("规则节点 = %d，期望 %d", got.TreeNodeID, node.ID)
	}
}
