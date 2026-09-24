package tree

import (
	"xuntai/internal/model"

	"gorm.io/gorm"
)

// CanWrite 沿父节点往上找运维负责人。研发负责人不能写。
func CanWrite(db *gorm.DB, userID, nodeID uint) (bool, error) {
	return hasOwner(db, userID, nodeID, []string{"ops"})
}

// OnNode 是这个节点或上级的运维、研发负责人。用来提交工单，不能用来改树。
func OnNode(db *gorm.DB, userID, nodeID uint) (bool, error) {
	return hasOwner(db, userID, nodeID, []string{"ops", "rd"})
}

func hasOwner(db *gorm.DB, userID, nodeID uint, kinds []string) (bool, error) {
	seen := map[uint]struct{}{}
	for {
		if _, ok := seen[nodeID]; ok {
			return false, nil
		}
		seen[nodeID] = struct{}{}
		var node model.Node
		if err := db.First(&node, nodeID).Error; err != nil {
			return false, err
		}
		var count int64
		err := db.Model(&model.NodeOwner{}).
			Where("node_id = ? AND user_id = ? AND kind IN ?", node.ID, userID, kinds).
			Count(&count).Error
		if err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
		if node.ParentID == nil {
			return false, nil
		}
		nodeID = *node.ParentID
	}
}

// SubtreeIDs 返回节点自己和全部后代。
func SubtreeIDs(db *gorm.DB, root uint) ([]uint, error) {
	var nodes []model.Node
	if err := db.Select("id", "parent_id").Find(&nodes).Error; err != nil {
		return nil, err
	}
	children := map[uint][]uint{}
	exists := false
	for _, node := range nodes {
		if node.ID == root {
			exists = true
		}
		if node.ParentID != nil {
			children[*node.ParentID] = append(children[*node.ParentID], node.ID)
		}
	}
	if !exists {
		return nil, gorm.ErrRecordNotFound
	}
	out := make([]uint, 0, 4)
	stack := []uint{root}
	seen := map[uint]struct{}{}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		stack = append(stack, children[id]...)
	}
	return out, nil
}
