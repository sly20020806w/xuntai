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
)

var defaultStages = []string{"构建", "预发", "生产"}

var reservedStep = map[string]struct{}{
	"gate": {}, "deploy": {}, "confirm": {}, "verify": {},
}

// Attr 是发布项对象上的配置。执行器留空，平台不调用灰度组件。
type Attr struct {
	Repo      string   `json:"repo"`
	Image     string   `json:"image"`
	Stages    []string `json:"stages"`
	Clusters  []uint   `json:"clusters"`
	Batches   []string `json:"batches"`
	Strategy  string   `json:"strategy"`
	VerifyURL string   `json:"verify_url"`
	Executor  string   `json:"executor"`
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
	attr.Executor = ""
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
	attr, err := Load(db, itemID)
	if err != nil {
		return nil, "", err
	}
	batches := clean(attr.Batches)
	if len(batches) < 2 {
		batches = nil
	}
	return batches, strings.TrimSpace(attr.VerifyURL), nil
}

// Validate 拒绝还没接上的执行器，以及不能当步骤名的批次。
func Validate(attr Attr) error {
	return checkAttr(attr)
}

func checkAttr(attr Attr) error {
	if strings.TrimSpace(attr.Executor) != "" {
		return ErrExecutor
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
