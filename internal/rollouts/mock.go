package rollouts

import "sync"

// Stage 是某个实例当前的灰度进度。只在模拟打开时写入。
type Stage struct {
	Weight   int
	Image    string
	Previous string
	Aborted  bool
}

var (
	mu     sync.Mutex
	stages = map[uint]Stage{}
)

// Remember 记下模拟出来的权重。真实集群路径不会调用它。
func Remember(instanceID uint, stage Stage) {
	mu.Lock()
	defer mu.Unlock()
	stages[instanceID] = stage
}

// Current 读取模拟进度。没有记录时权重是 0。
func Current(instanceID uint) Stage {
	mu.Lock()
	defer mu.Unlock()
	return stages[instanceID]
}

// Reset 清掉一个实例的模拟进度，测试用来确认真实路径没有写它。
func Reset(instanceID uint) {
	mu.Lock()
	defer mu.Unlock()
	delete(stages, instanceID)
}
