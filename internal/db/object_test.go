package db

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"xuntai/internal/model"
)

func TestEnsureKeepsInstance(t *testing.T) {
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
	row := model.Instance{
		Name: "订单主库", TreeNodeID: node.ID, Host: "10.8.2.17", Port: 3306, Version: "8.0", Role: "主",
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureAll(db); err != nil {
		t.Fatal(err)
	}
	if err := EnsureAll(db); err != nil {
		t.Fatal(err)
	}
	var again model.Instance
	if err := db.First(&again, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if again.ObjectID == 0 || again.Env != "生产" || again.TreeNodeID != node.ID {
		t.Fatalf("对象或环境不对 %+v", again)
	}
	var objects int64
	if err := db.Model(&model.CMDBObject{}).Where("name = ?", "订单主库").Count(&objects).Error; err != nil {
		t.Fatal(err)
	}
	if objects != 1 {
		t.Fatalf("对象数 = %d", objects)
	}
	var link model.ObjectNode
	if err := db.Where("object_id = ? AND node_id = ?", again.ObjectID, node.ID).First(&link).Error; err != nil {
		t.Fatal(err)
	}
}
