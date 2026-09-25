package db

import (
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"

	"xuntai/internal/model"
)

const ModelCode = "db_instance"

// Attr 是实例对象上的稳态配置。运行状态不写在这里，口令也不写。
type Attr struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Version     string `json:"version"`
	Role        string `json:"role"`
	Env         string `json:"env"`
	LoginUser   string `json:"loginUser"`
	MonitorAddr string `json:"monitorAddr"`
	MasterID    uint   `json:"masterId"`
}

// EnsureAll 给还没有对象的实例补上对象，并挂到原来的叶子。已有实例编号不变。
func EnsureAll(db *gorm.DB) error {
	var rows []model.Instance
	if err := db.Find(&rows).Error; err != nil {
		return err
	}
	for i := range rows {
		if err := Ensure(db, &rows[i]); err != nil {
			return err
		}
	}
	return nil
}

// Ensure 保证实例有一条 CMDB 对象，并挂在它的叶子上。对象已在时只更新配置、不另建。
func Ensure(db *gorm.DB, row *model.Instance) error {
	if strings.TrimSpace(row.Env) == "" {
		row.Env = "生产"
		if row.ID != 0 {
			if err := db.Model(row).Update("env", row.Env).Error; err != nil {
				return err
			}
		}
	}
	attr := Attr{
		Host: row.Host, Port: row.Port, Version: row.Version, Role: row.Role,
		Env: row.Env, LoginUser: row.LoginUser, MonitorAddr: row.MonitorAddr, MasterID: row.MasterID,
	}
	raw, err := json.Marshal(attr)
	if err != nil {
		return err
	}
	if row.ObjectID != 0 {
		var object model.CMDBObject
		err := db.First(&object, row.ObjectID).Error
		if err == nil {
			if err := db.Model(&object).Update("attr_json", string(raw)).Error; err != nil {
				return err
			}
			return link(db, object.ID, row.TreeNodeID)
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	modelID, err := instanceModel(db)
	if err != nil {
		return err
	}
	object := model.CMDBObject{
		ModelID: modelID, Name: row.Name, TreeNodeID: row.TreeNodeID, AttrJSON: string(raw),
	}
	if err := db.Create(&object).Error; err != nil {
		return err
	}
	if err := link(db, object.ID, row.TreeNodeID); err != nil {
		return err
	}
	if row.ID != 0 {
		if err := db.Model(row).Update("object_id", object.ID).Error; err != nil {
			return err
		}
	}
	row.ObjectID = object.ID
	return nil
}

func instanceModel(db *gorm.DB) (uint, error) {
	var row model.CMDBModel
	err := db.Where("code = ?", ModelCode).First(&row).Error
	if err == nil {
		return row.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	row = model.CMDBModel{Name: "MySQL 实例", Code: ModelCode, Remark: "主机、端口、版本、角色和连接配置，不记口令和运行状态"}
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
