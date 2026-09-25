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

// OpsOwners 从当前节点往根走，按由近到远返回运维负责人。同一个人只出现一次。
func OpsOwners(db *gorm.DB, nodeID uint) ([]model.User, error) {
	seenNode := map[uint]struct{}{}
	seenUser := map[uint]struct{}{}
	ids := make([]uint, 0, 2)
	for {
		if _, ok := seenNode[nodeID]; ok {
			break
		}
		seenNode[nodeID] = struct{}{}
		var node model.Node
		if err := db.First(&node, nodeID).Error; err != nil {
			return nil, err
		}
		var owners []model.NodeOwner
		if err := db.Where("node_id = ? AND kind = ?", node.ID, "ops").Order("id").Find(&owners).Error; err != nil {
			return nil, err
		}
		for _, owner := range owners {
			if _, ok := seenUser[owner.UserID]; ok {
				continue
			}
			seenUser[owner.UserID] = struct{}{}
			ids = append(ids, owner.UserID)
		}
		if node.ParentID == nil {
			break
		}
		nodeID = *node.ParentID
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var users []model.User
	if err := db.Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]model.User, len(users))
	for _, user := range users {
		byID[user.ID] = user
	}
	out := make([]model.User, 0, len(ids))
	for _, id := range ids {
		if user, ok := byID[id]; ok {
			out = append(out, user)
		}
	}
	return out, nil
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
