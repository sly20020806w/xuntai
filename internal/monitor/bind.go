package monitor

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"xuntai/internal/model"
	"xuntai/internal/tree"
)

var (
	ErrObjectNode = errors.New("对象不在这个节点下")
	ErrAppNode    = errors.New("应用不在这个节点下")
	ErrNoObject   = errors.New("没有这个对象")
	ErrNoApp      = errors.New("没有这个应用")
	ErrObjectTree = errors.New("对象没有挂到服务树")
)

// Place 确认可选的应用和对象都挂在这个节点或其子节点上。
func Place(db *gorm.DB, nodeID, appID, objectID uint) error {
	if appID != 0 {
		if err := appOnNode(db, nodeID, appID); err != nil {
			return err
		}
	}
	if objectID != 0 {
		if err := objectOnNode(db, nodeID, objectID); err != nil {
			return err
		}
	}
	return nil
}

func appOnNode(db *gorm.DB, nodeID, appID uint) error {
	var app model.App
	if err := db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNoApp
		}
		return err
	}
	var project model.Project
	if err := db.First(&project, app.ProjectID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNoApp
		}
		return err
	}
	ok, err := nodeCovers(db, nodeID, project.TreeNodeID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrAppNode
	}
	return nil
}

func objectOnNode(db *gorm.DB, nodeID, objectID uint) error {
	var object model.CMDBObject
	if err := db.First(&object, objectID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNoObject
		}
		return err
	}
	if object.TreeNodeID != 0 {
		ok, err := nodeCovers(db, nodeID, object.TreeNodeID)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	ids, err := tree.SubtreeIDs(db, nodeID)
	if err != nil {
		return err
	}
	var n int64
	if err := db.Model(&model.ObjectNode{}).Where("object_id = ? AND node_id IN ?", objectID, ids).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if object.TreeNodeID == 0 {
		return ErrObjectTree
	}
	return ErrObjectNode
}

func nodeCovers(db *gorm.DB, root, target uint) (bool, error) {
	if target == 0 {
		return false, nil
	}
	ids, err := tree.SubtreeIDs(db, root)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if id == target {
			return true, nil
		}
	}
	return false, nil
}

// Locate 用对象或节点确定告警落在哪。对象必须挂在给出的节点下。
func Locate(db *gorm.DB, nodeID, objectID uint) (uint, error) {
	if objectID == 0 {
		if nodeID == 0 {
			return 0, fmt.Errorf("需要节点")
		}
		var node model.Node
		if err := db.First(&node, nodeID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, fmt.Errorf("没有这个节点")
			}
			return 0, err
		}
		return node.ID, nil
	}
	var object model.CMDBObject
	if err := db.First(&object, objectID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrNoObject
		}
		return 0, err
	}
	if object.TreeNodeID == 0 {
		return 0, ErrObjectTree
	}
	if nodeID != 0 {
		ok, err := nodeCovers(db, nodeID, object.TreeNodeID)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, ErrObjectNode
		}
	}
	return object.TreeNodeID, nil
}
