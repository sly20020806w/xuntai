package scope

import (
	"sync"

	"gorm.io/gorm"

	"xuntai/internal/model"
	"xuntai/internal/tree"
)

// 默认按服务树收窄列表。XUNTAI_SCOPE_FILTER=0 时回到全量，给单租户用。
var (
	mu      sync.RWMutex
	enabled = true
)

func SetEnabled(on bool) {
	mu.Lock()
	enabled = on
	mu.Unlock()
}

func Enabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return enabled
}

// IDs 返回这个人作为运维或研发负责人能看见的节点，含子节点。
// all 为 true 表示不过滤。
func IDs(db *gorm.DB, userID uint) (ids []uint, all bool, err error) {
	if !Enabled() {
		return nil, true, nil
	}
	var owned []model.NodeOwner
	if err = db.Where("user_id = ? AND kind IN ?", userID, []string{"ops", "rd"}).Find(&owned).Error; err != nil {
		return nil, false, err
	}
	seen := map[uint]struct{}{}
	for _, row := range owned {
		sub, err := tree.SubtreeIDs(db, row.NodeID)
		if err != nil {
			return nil, false, err
		}
		for _, id := range sub {
			seen[id] = struct{}{}
		}
	}
	ids = make([]uint, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids, false, nil
}

// Limit 把查询限制在可见节点上。column 是树节点列名。
func Limit(db *gorm.DB, userID uint, query *gorm.DB, column string) (*gorm.DB, error) {
	ids, all, err := IDs(db, userID)
	if err != nil || all {
		return query, err
	}
	if len(ids) == 0 {
		return query.Where("1 = 0"), nil
	}
	return query.Where(column+" IN ?", ids), nil
}

func Allows(ids []uint, all bool, nodeID uint) bool {
	if all {
		return true
	}
	for _, id := range ids {
		if id == nodeID {
			return true
		}
	}
	return false
}
