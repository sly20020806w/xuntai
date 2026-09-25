package cmdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xuntai/internal/model"
	"xuntai/internal/scope"
	"xuntai/internal/tree"
)

type Deps struct {
	DB *gorm.DB
}

func Register(r *gin.RouterGroup, deps Deps) {
	h := handler{deps: deps}
	r.GET("/models", h.listModels)
	r.POST("/models", h.createModel)
	r.PUT("/models/:id", h.updateModel)
	r.DELETE("/models/:id", h.deleteModel)
	r.GET("/objects", h.listObjects)
	r.POST("/objects", h.createObject)
	r.PUT("/objects/:id", h.updateObject)
	r.DELETE("/objects/:id", h.deleteObject)
	r.GET("/object-nodes", h.listLinks)
	r.POST("/object-nodes", h.createLink)
	r.DELETE("/object-nodes/:id", h.deleteLink)
}

type handler struct {
	deps Deps
}

func (h handler) listModels(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var rows []model.CMDBModel
	if err := h.deps.DB.Order("id").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "模型读取失败"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{"id": row.ID, "name": row.Name, "code": row.Code, "remark": row.Remark})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createModel(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		Name   string `json:"name"`
		Code   string `json:"code"`
		Remark string `json:"remark"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Code) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要模型名和编码"})
		return
	}
	row := model.CMDBModel{Name: strings.TrimSpace(body.Name), Code: strings.TrimSpace(body.Code), Remark: strings.TrimSpace(body.Remark)}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模型没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": row.Name, "code": row.Code})
}

func (h handler) updateModel(c *gin.Context) {
	row, ok := h.findModel(c)
	if !ok {
		return
	}
	var body struct {
		Name   string `json:"name"`
		Remark string `json:"remark"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要模型名"})
		return
	}
	if err := h.deps.DB.Model(&row).Updates(map[string]any{"name": strings.TrimSpace(body.Name), "remark": strings.TrimSpace(body.Remark)}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "模型没有改成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": strings.TrimSpace(body.Name)})
}

func (h handler) deleteModel(c *gin.Context) {
	row, ok := h.findModel(c)
	if !ok {
		return
	}
	var n int64
	if err := h.deps.DB.Model(&model.CMDBObject{}).Where("model_id = ?", row.ID).Count(&n).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "模型没有删掉"})
		return
	}
	if n > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模型下面还有对象"})
		return
	}
	if err := h.deps.DB.Delete(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "模型没有删掉"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID})
}

func (h handler) listObjects(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	query, err := scope.Limit(h.deps.DB, c.GetUint("uid"), h.deps.DB.Order("id"), "tree_node_id")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	var rows []model.CMDBObject
	if err := query.Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "对象读取失败"})
		return
	}
	names := h.nodeNames()
	models := h.modelNames()
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{
			"id": row.ID, "name": row.Name, "modelId": row.ModelID, "modelName": models[row.ModelID],
			"nodeId": row.TreeNodeID, "nodeName": names[row.TreeNodeID], "attr": json.RawMessage(blank(row.AttrJSON)),
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createObject(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		ModelID    uint           `json:"modelId"`
		Name       string         `json:"name"`
		TreeNodeID uint           `json:"treeNodeId"`
		Attr       map[string]any `json:"attr"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" || body.ModelID == 0 || body.TreeNodeID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要模型、名称和节点"})
		return
	}
	if !h.ops(c, body.TreeNodeID) {
		return
	}
	if err := h.deps.DB.First(&model.CMDBModel{}, body.ModelID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个模型"})
		return
	}
	raw, err := configJSON(body.Attr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row := model.CMDBObject{ModelID: body.ModelID, Name: strings.TrimSpace(body.Name), TreeNodeID: body.TreeNodeID, AttrJSON: raw}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "对象没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": row.Name, "nodeId": row.TreeNodeID})
}

func (h handler) updateObject(c *gin.Context) {
	row, ok := h.findObject(c)
	if !ok || !h.ops(c, row.TreeNodeID) {
		return
	}
	var body struct {
		Name string         `json:"name"`
		Attr map[string]any `json:"attr"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要对象名"})
		return
	}
	raw, err := configJSON(body.Attr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.deps.DB.Model(&row).Updates(map[string]any{"name": strings.TrimSpace(body.Name), "attr_json": raw}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "对象没有改成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": strings.TrimSpace(body.Name)})
}

func (h handler) deleteObject(c *gin.Context) {
	row, ok := h.findObject(c)
	if !ok || !h.ops(c, row.TreeNodeID) {
		return
	}
	err := h.deps.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("object_id = ?", row.ID).Delete(&model.ObjectNode{}).Error; err != nil {
			return err
		}
		return tx.Delete(&row).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "对象没有删掉"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID})
}

func (h handler) listLinks(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var rows []model.ObjectNode
	if err := h.deps.DB.Order("id").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "关系读取失败"})
		return
	}
	ids, all, err := scope.IDs(h.deps.DB, c.GetUint("uid"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		if !scope.Allows(ids, all, row.NodeID) {
			continue
		}
		out = append(out, gin.H{"id": row.ID, "objectId": row.ObjectID, "nodeId": row.NodeID})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createLink(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		ObjectID uint `json:"objectId"`
		NodeID   uint `json:"nodeId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.ObjectID == 0 || body.NodeID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要对象和节点"})
		return
	}
	if !h.ops(c, body.NodeID) {
		return
	}
	if err := h.deps.DB.First(&model.CMDBObject{}, body.ObjectID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个对象"})
		return
	}
	if err := h.deps.DB.First(&model.Node{}, body.NodeID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个节点"})
		return
	}
	row := model.ObjectNode{ObjectID: body.ObjectID, NodeID: body.NodeID}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "关系没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "objectId": row.ObjectID, "nodeId": row.NodeID})
}

func (h handler) deleteLink(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	var row model.ObjectNode
	if !h.ready(c) || err != nil || id <= 0 || h.deps.DB.First(&row, id).Error != nil {
		if h.deps.DB != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "没有这条关系"})
		}
		return
	}
	if !h.ops(c, row.NodeID) {
		return
	}
	if err := h.deps.DB.Delete(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "关系没有删掉"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID})
}

func (h handler) findModel(c *gin.Context) (model.CMDBModel, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	var row model.CMDBModel
	if !h.ready(c) || err != nil || id <= 0 || h.deps.DB.First(&row, id).Error != nil {
		if h.deps.DB != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "没有这个模型"})
		}
		return row, false
	}
	return row, true
}

func (h handler) findObject(c *gin.Context) (model.CMDBObject, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	var row model.CMDBObject
	if !h.ready(c) || err != nil || id <= 0 || h.deps.DB.First(&row, id).Error != nil {
		if h.deps.DB != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "没有这个对象"})
		}
		return row, false
	}
	return row, true
}

func (h handler) ops(c *gin.Context, nodeID uint) bool {
	ok, err := tree.CanWrite(h.deps.DB, c.GetUint("uid"), nodeID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个节点"})
		return false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return false
	}
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "没有这个节点的运维权限"})
		return false
	}
	return true
}

func (h handler) ready(c *gin.Context) bool {
	if h.deps.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "数据库还没有准备好"})
		return false
	}
	return true
}

func (h handler) nodeNames() map[uint]string {
	names := map[uint]string{}
	var nodes []model.Node
	_ = h.deps.DB.Find(&nodes).Error
	for _, node := range nodes {
		names[node.ID] = node.Name
	}
	return names
}

func (h handler) modelNames() map[uint]string {
	names := map[uint]string{}
	var rows []model.CMDBModel
	_ = h.deps.DB.Find(&rows).Error
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	return names
}

func configJSON(attr map[string]any) (string, error) {
	if attr == nil {
		attr = map[string]any{}
	}
	for _, key := range []string{"status", "health", "ready", "phase"} {
		if _, ok := attr[key]; ok {
			return "", errors.New("对象只记配置，不记运行状态")
		}
	}
	raw, err := json.Marshal(attr)
	if err != nil {
		return "", errors.New("配置没有写成")
	}
	return string(raw), nil
}

func blank(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "{}"
	}
	return raw
}
