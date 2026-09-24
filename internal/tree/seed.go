package tree

import (
	"errors"

	"xuntai/internal/base"
	"xuntai/internal/model"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func APICatalog() []model.API {
	return []model.API{
		{Method: "GET", Path: "/api/tree/nodes"},
		{Method: "GET", Path: "/api/tree/machines"},
		{Method: "POST", Path: "/api/tree/nodes"},
		{Method: "POST", Path: "/api/tree/nodes/:id/machines"},
		{Method: "PUT", Path: "/api/tree/nodes/:id/owners"},
	}
}

// Seed 补上服务树接口，并在树还是空的时候放入示例节点。
// 已有节点时不再改树。返回值表示这次有没有新建节点。
func Seed(db *gorm.DB, password string) (bool, error) {
	if err := base.Grant(db, "平台管理员", APICatalog()); err != nil {
		return false, err
	}
	var count int64
	if err := db.Model(&model.Node{}).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	if password == "" {
		return false, errors.New("必须设置 XUNTAI_ADMIN_PASSWORD")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		role := model.Role{Name: "节点运维"}
		if err := tx.Create(&role).Error; err != nil {
			return err
		}
		if err := base.Grant(tx, role.Name, append(APICatalog(),
			model.API{Method: "GET", Path: "/api/base/me"},
			model.API{Method: "GET", Path: "/api/base/menus"},
		)); err != nil {
			return err
		}
		var menu model.Menu
		if err := tx.Where("path = ?", "tree").First(&menu).Error; err != nil {
			return err
		}
		if err := tx.Model(&role).Association("Menus").Replace([]model.Menu{menu}); err != nil {
			return err
		}

		var zhou model.User
		if err := tx.Where("name = ?", "周宁").First(&zhou).Error; err != nil {
			return err
		}
		lin, err := ensureUser(tx, "林夏", string(hash), role)
		if err != nil {
			return err
		}
		xu, err := ensureUser(tx, "许衡", string(hash), role)
		if err != nil {
			return err
		}
		chen, err := ensureUser(tx, "陈舟", string(hash), role)
		if err != nil {
			return err
		}

		arch, err := makeNode(tx, "基础架构", nil, false, 0)
		if err != nil {
			return err
		}
		observe, err := makeNode(tx, "可观测", &arch.ID, true, 1)
		if err != nil {
			return err
		}
		edge, err := makeNode(tx, "入口", &arch.ID, true, 1)
		if err != nil {
			return err
		}
		trade, err := makeNode(tx, "交易", nil, false, 0)
		if err != nil {
			return err
		}
		order, err := makeNode(tx, "订单", &trade.ID, true, 1)
		if err != nil {
			return err
		}
		pay, err := makeNode(tx, "支付", &trade.ID, true, 1)
		if err != nil {
			return err
		}

		owners := []model.NodeOwner{
			{NodeID: arch.ID, UserID: zhou.ID, Kind: "ops"},
			{NodeID: observe.ID, UserID: zhou.ID, Kind: "ops"},
			{NodeID: observe.ID, UserID: xu.ID, Kind: "ops"},
			{NodeID: edge.ID, UserID: zhou.ID, Kind: "ops"},
			{NodeID: trade.ID, UserID: lin.ID, Kind: "ops"},
			{NodeID: order.ID, UserID: lin.ID, Kind: "ops"},
			{NodeID: order.ID, UserID: chen.ID, Kind: "rd"},
			{NodeID: pay.ID, UserID: lin.ID, Kind: "ops"},
		}
		if err := tx.Create(&owners).Error; err != nil {
			return err
		}
		machines := []struct {
			node                   model.Node
			name, ip, vendor, spec string
		}{
			{observe, "obs-a-01", "10.4.1.21", "自建", "16C 64G"},
			{observe, "obs-a-02", "10.4.1.22", "自建", "16C 64G"},
			{edge, "edge-b-03", "10.4.8.11", "公有云", "8C 32G"},
			{order, "order-c-07", "10.8.2.17", "公有云", "8C 16G"},
			{order, "order-c-08", "10.8.2.18", "公有云", "8C 16G"},
			{pay, "pay-c-02", "10.8.3.9", "公有云", "8C 16G"},
		}
		for _, item := range machines {
			if err := bindMachine(tx, item.node, item.name, item.ip, item.vendor, item.spec); err != nil {
				return err
			}
		}
		return nil
	})
	return err == nil, err
}

func ensureUser(tx *gorm.DB, name, hash string, role model.Role) (model.User, error) {
	user := model.User{Name: name, PasswordHash: hash}
	if err := tx.Create(&user).Error; err != nil {
		return user, err
	}
	err := tx.Model(&user).Association("Roles").Replace([]model.Role{role})
	return user, err
}

func makeNode(tx *gorm.DB, name string, parentID *uint, leaf bool, level int) (model.Node, error) {
	node := model.Node{Name: name, ParentID: parentID, IsLeaf: leaf, Level: level}
	return node, tx.Create(&node).Error
}

func bindMachine(tx *gorm.DB, node model.Node, name, ip, vendor, spec string) error {
	machine := model.Machine{Name: name, IP: ip, Vendor: vendor, Spec: spec}
	if err := tx.Create(&machine).Error; err != nil {
		return err
	}
	return tx.Model(&machine).Association("Nodes").Append(&node)
}
