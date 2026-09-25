package cicd

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"xuntai/internal/model"
)

func TestEnsureKeepsOrders(t *testing.T) {
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
	item := model.DeployItem{Name: "order-api", TreeNodeID: node.ID, Repo: "git.local/order-api", ImageName: "order-api"}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	order := model.ReleaseOrder{ItemID: item.ID, Tag: "1.8.4", Env: "生产", Status: "running"}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	stage := model.ReleaseStage{OrderID: order.ID, Name: "生产", Seq: 3, Status: "pending"}
	if err := db.Create(&stage).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureAll(db); err != nil {
		t.Fatal(err)
	}
	var got model.DeployItem
	if err := db.First(&got, item.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.ObjectID == 0 || got.Name != "order-api" || got.Repo != "git.local/order-api" {
		t.Fatalf("发布项 = %+v", got)
	}
	var kept model.ReleaseOrder
	if err := db.First(&kept, order.ID).Error; err != nil || kept.ItemID != item.ID || kept.Tag != "1.8.4" {
		t.Fatalf("发布单 = %+v %v", kept, err)
	}
	var object model.CMDBObject
	if err := db.First(&object, got.ObjectID).Error; err != nil {
		t.Fatal(err)
	}
	if object.TreeNodeID != node.ID || object.Name != "order-api" {
		t.Fatalf("对象 = %+v", object)
	}
	var link model.ObjectNode
	if err := db.Where("object_id = ? AND node_id = ?", object.ID, node.ID).First(&link).Error; err != nil {
		t.Fatal(err)
	}
	attr, err := Load(db, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if attr.Image != "order-api" || attr.Strategy != "manual" || attr.Executor != "" {
		t.Fatalf("策略 = %+v", attr)
	}
}
