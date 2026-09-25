package cicd

import (
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"

	"xuntai/internal/model"
)

const ModelCode = "release_item"

var (
	ErrExecutor = errors.New("执行器还没有接上")
	ErrBatch    = errors.New("批次名称不对")
	ErrStrategy = errors.New("灰度百分比要从 1 到 100，并且以 100 结束")
)

var defaultStages = []string{"构建", "预发", "生产"}

var reservedStep = map[string]struct{}{
	"gate": {}, "deploy": {}, "confirm": {}, "verify": {},
}

// Attr 是发布项对象上的配置。执行器留空，平台不调用灰度组件。
type Attr struct {
	Repo             string   `json:"repo"`
	Image            string   `json:"image"`
	Stages           []string `json:"stages"`
	Clusters         []uint   `json:"clusters"`
	Batches          []string `json:"batches"`
	Strategy         string   `json:"strategy"`
	VerifyURL        string   `json:"verify_url"`
	Executor         string   `json:"executor"`
	Weights          []int    `json:"weights"`
	StableSeconds    int      `json:"stableSeconds"`
	FailureThreshold int      `json:"failureThreshold"`
	AutoRollback     bool     `json:"autoRollback"`
}

// Spec 是启动剧本时从发布项读到的策略。回滚由剧本编码决定，不看入参。
type Spec struct {
	Executor         string
	Strategy         string
	Batches          []string
	Verify           string
	Weights          []int
	StableSeconds    int
	FailureThreshold int
	AutoRollback     bool
	Rollback         bool
}

// EnsureAll 给还没有对象的发布项补上对象和叶子关系。已有发布项和发布单保持原编号。
func EnsureAll(db *gorm.DB) error {
	var items []model.DeployItem
	if err := db.Find(&items).Error; err != nil {
		return err
	}
	for i := range items {
		if err := Ensure(db, &items[i]); err != nil {
			return err
		}
	}
	return nil
}

// Ensure 保证发布项有一条 CMDB 对象，并挂在它的叶子上。
func Ensure(db *gorm.DB, item *model.DeployItem) error {
	if item.ObjectID != 0 {
		var object model.CMDBObject
		err := db.First(&object, item.ObjectID).Error
		if err == nil {
			return link(db, object.ID, item.TreeNodeID)
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	modelID, err := releaseModel(db)
	if err != nil {
		return err
	}
	attr := Attr{
		Repo: item.Repo, Image: item.ImageName,
		Stages: append([]string{}, defaultStages...), Strategy: "manual",
	}
	raw, err := json.Marshal(attr)
	if err != nil {
		return err
	}
	object := model.CMDBObject{
		ModelID: modelID, Name: item.Name, TreeNodeID: item.TreeNodeID, AttrJSON: string(raw),
	}
	if err := db.Create(&object).Error; err != nil {
		return err
	}
	if err := link(db, object.ID, item.TreeNodeID); err != nil {
		return err
	}
	if err := db.Model(item).Update("object_id", object.ID).Error; err != nil {
		return err
	}
	item.ObjectID = object.ID
	return nil
}

// Save 把发布策略写回对象。仓库和镜像仍以发布项列为准。
func Save(db *gorm.DB, item *model.DeployItem, attr Attr) error {
	if err := checkAttr(attr); err != nil {
		return err
	}
	if err := Ensure(db, item); err != nil {
		return err
	}
	attr.Repo = item.Repo
	attr.Image = item.ImageName
	attr.Executor = strings.TrimSpace(attr.Executor)
	if attr.Strategy == "" {
		attr.Strategy = "manual"
	}
	if len(attr.Stages) == 0 {
		attr.Stages = append([]string{}, defaultStages...)
	}
	attr.Batches = clean(attr.Batches)
	attr.VerifyURL = strings.TrimSpace(attr.VerifyURL)
	raw, err := json.Marshal(attr)
	if err != nil {
		return err
	}
	return db.Model(&model.CMDBObject{}).Where("id = ?", item.ObjectID).Update("attr_json", string(raw)).Error
}

// Load 读发布项对象上的策略。还没有对象时返回空策略。
func Load(db *gorm.DB, itemID uint) (Attr, error) {
	var attr Attr
	var item model.DeployItem
	if err := db.First(&item, itemID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return attr, nil
		}
		return attr, err
	}
	if item.ObjectID == 0 {
		return attr, nil
	}
	var object model.CMDBObject
	if err := db.First(&object, item.ObjectID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return attr, nil
		}
		return attr, err
	}
	if strings.TrimSpace(object.AttrJSON) == "" {
		return attr, nil
	}
	if err := json.Unmarshal([]byte(object.AttrJSON), &attr); err != nil {
		return Attr{}, err
	}
	return attr, nil
}

// Plan 给出要展开的批次和校验地址。批次不足两条时不展开，沿用剧本里的 confirm。
func Plan(db *gorm.DB, itemID uint) ([]string, string, error) {
	spec, err := SpecFor(db, itemID)
	if err != nil {
		return nil, "", err
	}
	return spec.Batches, spec.Verify, nil
}

// SpecFor 从发布项对象读执行器和灰度策略。入参里的同名字段不在这里。
func SpecFor(db *gorm.DB, itemID uint) (Spec, error) {
	var spec Spec
	attr, err := Load(db, itemID)
	if err != nil {
		return spec, err
	}
	name := ExecutorName(attr)
	if name != "platform" && name != "rollouts" {
		return spec, ErrExecutor
	}
	spec.Executor = name
	spec.Strategy = strings.TrimSpace(attr.Strategy)
	spec.Batches = clean(attr.Batches)
	if len(spec.Batches) < 2 {
		spec.Batches = nil
	}
	spec.Verify = strings.TrimSpace(attr.VerifyURL)
	spec.Weights = append([]int{}, attr.Weights...)
	spec.StableSeconds = attr.StableSeconds
	spec.FailureThreshold = attr.FailureThreshold
	if spec.FailureThreshold <= 0 {
		spec.FailureThreshold = 1
	}
	spec.AutoRollback = attr.AutoRollback
	return spec, nil
}

// ExecutorName 空和 platform 都是现有的一次写完整镜像。
func ExecutorName(attr Attr) string {
	switch strings.TrimSpace(attr.Executor) {
	case "", "platform":
		return "platform"
	default:
		return strings.TrimSpace(attr.Executor)
	}
}

// Stages 是灰度要走过的百分比。没写权重时，canary 用 10、50、100；batch 按批次均分。
func (s Spec) Stages() []int {
	if s.Executor != "rollouts" || s.Rollback {
		return nil
	}
	if len(s.Weights) > 0 {
		return append([]int{}, s.Weights...)
	}
	if s.Strategy == "batch" && len(s.Batches) > 0 {
		n := len(s.Batches)
		out := make([]int, n)
		for i := range out {
			out[i] = (i + 1) * 100 / n
		}
		out[n-1] = 100
		return out
	}
	return []int{10, 50, 100}
}

// Validate 拒绝还没接上的执行器，以及不能当步骤名的批次。
func Validate(attr Attr) error {
	return checkAttr(attr)
}

func checkAttr(attr Attr) error {
	switch strings.TrimSpace(attr.Executor) {
	case "", "platform", "rollouts":
	default:
		return ErrExecutor
	}
	if attr.StableSeconds < 0 || attr.FailureThreshold < 0 {
		return ErrStrategy
	}
	if len(attr.Weights) > 0 {
		for _, weight := range attr.Weights {
			if weight < 1 || weight > 100 {
				return ErrStrategy
			}
		}
		if strings.TrimSpace(attr.Executor) == "rollouts" && attr.Weights[len(attr.Weights)-1] != 100 {
			return ErrStrategy
		}
	}
	seen := map[string]struct{}{}
	for _, name := range attr.Batches {
		name = strings.TrimSpace(name)
		if name == "" {
			return ErrBatch
		}
		if _, reserved := reservedStep[name]; reserved {
			return ErrBatch
		}
		if _, ok := seen[name]; ok {
			return ErrBatch
		}
		seen[name] = struct{}{}
	}
	return nil
}

func clean(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	return out
}

func releaseModel(db *gorm.DB) (uint, error) {
	var row model.CMDBModel
	err := db.Where("code = ?", ModelCode).First(&row).Error
	if err == nil {
		return row.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	row = model.CMDBModel{Name: "发布项", Code: ModelCode, Remark: "仓库、镜像、阶段、集群和发布策略"}
	if err := db.Create(&row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

func link(db *gorm.DB, objectID, nodeID uint) error {
	if objectID == 0 || nodeID == 0 {
		return nil
	}
	var row model.ObjectNode
	err := db.Where("object_id = ? AND node_id = ?", objectID, nodeID).First(&row).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return db.Create(&model.ObjectNode{ObjectID: objectID, NodeID: nodeID}).Error
}
