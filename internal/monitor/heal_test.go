package monitor

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"xuntai/internal/model"
)

func TestRenderKeepsAlertContext(t *testing.T) {
	raw := `{"tree_node_id":1,"service":"order","note":"处理 {{rule_name}} {{summary}}","host":"{{tree_node_id}}"}`
	got, err := Render(raw, AlertVars{
		TreeNodeID: 9, ObjectID: 4, RuleName: "磁盘", Fingerprint: "fp-1", Summary: "满了",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["tree_node_id"] != uint(9) || got["object_id"] != uint(4) || got["rule_name"] != "磁盘" || got["fingerprint"] != "fp-1" || got["summary"] != "满了" {
		t.Fatalf("上下文被模板改掉 %+v", got)
	}
	if got["service"] != "order" || got["note"] != "处理 磁盘 满了" || got["host"] != uint(9) {
		t.Fatalf("模板字段 = %+v", got)
	}
	plain, err := Render("  ", AlertVars{TreeNodeID: 2, RuleName: "失联"})
	if err != nil || plain["tree_node_id"] != uint(2) || plain["rule_name"] != "失联" {
		t.Fatalf("空模板 = %+v %v", plain, err)
	}
	if _, err := Render(`["nope"]`, AlertVars{}); err != ErrTemplate {
		t.Fatalf("数组模板 = %v", err)
	}
	if _, err := Render(`{"note":"{{nope}}"}`, AlertVars{}); err != ErrTemplateVar {
		t.Fatalf("未知变量 = %v", err)
	}
}

func TestCheckBindingAndRuleFit(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.Migrate(db); err != nil {
		t.Fatal(err)
	}
	parent := model.Node{Name: "父"}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	child := model.Node{Name: "子", ParentID: &parent.ID}
	other := model.Node{Name: "旁"}
	if err := db.Create(&child).Error; err != nil || db.Create(&other).Error != nil {
		t.Fatal(err)
	}
	published := model.Playbook{Code: "heal.note", Name: "记一笔", Status: "published", InputSchema: "{}"}
	draft := model.Playbook{Code: "heal.draft", Name: "草稿", Status: "draft", InputSchema: "{}"}
	if err := db.Create(&published).Error; err != nil || db.Create(&draft).Error != nil {
		t.Fatal(err)
	}
	if err := CheckBinding(db, published.ID, `{"service":"order"}`); err != nil {
		t.Fatal(err)
	}
	if err := CheckBinding(db, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := CheckBinding(db, draft.ID, ""); err != ErrUnpublished {
		t.Fatalf("草稿 = %v", err)
	}
	if err := CheckBinding(db, 99999, ""); err != ErrNoPlaybook {
		t.Fatalf("缺失 = %v", err)
	}
	rule := model.AlertRule{TreeNodeID: parent.ID, ObjectID: 3}
	if err := RuleFits(db, rule, child.ID, 3); err != nil {
		t.Fatal(err)
	}
	if err := RuleFits(db, rule, other.ID, 3); err != ErrRuleNode {
		t.Fatalf("旁支 = %v", err)
	}
	if err := RuleFits(db, rule, child.ID, 8); err != ErrRuleObject {
		t.Fatalf("对象 = %v", err)
	}
}
