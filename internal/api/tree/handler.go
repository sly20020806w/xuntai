package tree

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xuntai/internal/model"
	"xuntai/internal/scope"
	treecore "xuntai/internal/tree"
)

type Deps struct {
	DB *gorm.DB
}

func Register(r *gin.RouterGroup, deps Deps) {
	h := handler{deps: deps}
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"module": "tree"})
	})
	r.GET("/nodes", h.listNodes)
	r.POST("/nodes", h.createNode)
	r.GET("/machines", h.listMachines)
	r.POST("/nodes/:id/machines", h.bindMachine)
	r.PUT("/nodes/:id/owners", h.replaceOwners)
}

type handler struct {
	deps Deps
}

type ownerView struct {
	UserID uint   `json:"userId"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
}

type nodeView struct {
	ID       uint        `json:"id"`
	Name     string      `json:"name"`
	ParentID *uint       `json:"parentId"`
	IsLeaf   bool        `json:"isLeaf"`
	Level    int         `json:"level"`
	Owners   []ownerView `json:"owners"`
}

type machineView struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	IP       string `json:"ip"`
	Vendor   string `json:"vendor"`
	Spec     string `json:"spec"`
	NodeID   uint   `json:"nodeId,omitempty"`
	NodeName string `json:"nodeName,omitempty"`
}

func (h handler) listNodes(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var nodes []model.Node
	if err := h.deps.DB.Order("id").Find(&nodes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "节点读取失败"})
		return
	}
	owners, err := h.ownerMap()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "负责人读取失败"})
		return
	}
	visible, all, err := scope.IDs(h.deps.DB, c.GetUint("uid"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	out := make([]nodeView, 0, len(nodes))
	for _, node := range nodes {
		if !scope.Allows(visible, all, node.ID) {
			continue
		}
		view := nodeView{
			ID: node.ID, Name: node.Name, ParentID: node.ParentID,
			IsLeaf: node.IsLeaf, Level: node.Level, Owners: owners[node.ID],
		}
		if view.Owners == nil {
			view.Owners = []ownerView{}
		}
		out = append(out, view)
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createNode(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		Name     string `json:"name"`
		ParentID uint   `json:"parentId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" || body.ParentID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要节点名和上级"})
		return
	}
	parent, ok := h.findNode(c, body.ParentID)
	if !ok {
		return
	}
	if !h.requireOps(c, parent.ID) {
		return
	}
	child := model.Node{}
	err := h.deps.DB.Transaction(func(tx *gorm.DB) error {
		var bound int64
		if err := tx.Table("tree_node_machines").Where("node_id = ?", parent.ID).Count(&bound).Error; err != nil {
			return err
		}
		if bound > 0 {
			return errLeafHasMachines
		}
		child = model.Node{
			Name: strings.TrimSpace(body.Name), ParentID: &parent.ID,
			IsLeaf: true, Level: parent.Level + 1,
		}
		if err := tx.Create(&child).Error; err != nil {
			return err
		}
		if parent.IsLeaf {
			return tx.Model(&model.Node{}).Where("id = ?", parent.ID).Update("is_leaf", false).Error
		}
		return nil
	})
	if errors.Is(err, errLeafHasMachines) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "叶子上已经有机器，不能再挂子节点"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "节点没有建成"})
		return
	}
	c.JSON(http.StatusOK, nodeView{
		ID: child.ID, Name: child.Name, ParentID: child.ParentID,
		IsLeaf: true, Level: child.Level, Owners: []ownerView{},
	})
}

func (h handler) listMachines(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	allowed, ok := h.machineScope(c)
	if !ok {
		return
	}
	var machines []model.Machine
	if err := h.deps.DB.Preload("Nodes").Order("id").Find(&machines).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "机器读取失败"})
		return
	}
	out := make([]machineView, 0, len(machines))
	for _, machine := range machines {
		view := machineView{
			ID: machine.ID, Name: machine.Name, IP: machine.IP,
			Vendor: machine.Vendor, Spec: machine.Spec,
		}
		if len(machine.Nodes) > 0 {
			view.NodeID = machine.Nodes[0].ID
			view.NodeName = machine.Nodes[0].Name
		}
		if allowed != nil && !allowed[view.NodeID] {
			continue
		}
		out = append(out, view)
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) bindMachine(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	node, ok := h.nodeFromParam(c)
	if !ok {
		return
	}
	if !h.requireOps(c, node.ID) {
		return
	}
	if !node.IsLeaf {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只有叶子节点能挂机器"})
		return
	}
	var body struct {
		Name   string `json:"name"`
		IP     string `json:"ip"`
		Vendor string `json:"vendor"`
		Spec   string `json:"spec"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要机器名"})
		return
	}
	machine := model.Machine{
		Name: strings.TrimSpace(body.Name), IP: strings.TrimSpace(body.IP),
		Vendor: strings.TrimSpace(body.Vendor), Spec: strings.TrimSpace(body.Spec),
	}
	err := h.deps.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&machine).Error; err != nil {
			return err
		}
		return tx.Model(&machine).Association("Nodes").Append(&node)
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "机器没有挂上"})
		return
	}
	c.JSON(http.StatusOK, machineView{
		ID: machine.ID, Name: machine.Name, IP: machine.IP,
		Vendor: machine.Vendor, Spec: machine.Spec, NodeID: node.ID, NodeName: node.Name,
	})
}

func (h handler) replaceOwners(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	node, ok := h.nodeFromParam(c)
	if !ok {
		return
	}
	if !h.requireOps(c, node.ID) {
		return
	}
	var body struct {
		Owners []struct {
			UserID uint   `json:"userId"`
			Kind   string `json:"kind"`
		} `json:"owners"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不对"})
		return
	}
	rows := make([]model.NodeOwner, 0, len(body.Owners))
	seen := map[string]bool{}
	for _, item := range body.Owners {
		if item.Kind != "ops" && item.Kind != "rd" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "负责人只能是运维或研发"})
			return
		}
		var user model.User
		if err := h.deps.DB.First(&user, item.UserID).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "没有这个用户"})
			return
		}
		key := strconv.FormatUint(uint64(item.UserID), 10) + ":" + item.Kind
		if seen[key] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "负责人重复了"})
			return
		}
		seen[key] = true
		rows = append(rows, model.NodeOwner{NodeID: node.ID, UserID: item.UserID, Kind: item.Kind})
	}
	err := h.deps.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("node_id = ?", node.ID).Delete(&model.NodeOwner{}).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "负责人没有改成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": node.ID})
}

func (h handler) ready(c *gin.Context) bool {
	if h.deps.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "数据库还没有准备好"})
		return false
	}
	return true
}

func (h handler) findNode(c *gin.Context, id uint) (model.Node, bool) {
	var node model.Node
	if err := h.deps.DB.First(&node, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个节点"})
		return node, false
	}
	return node, true
}

func (h handler) nodeFromParam(c *gin.Context) (model.Node, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个节点"})
		return model.Node{}, false
	}
	return h.findNode(c, uint(id))
}

func (h handler) requireOps(c *gin.Context, nodeID uint) bool {
	ok, err := treecore.CanWrite(h.deps.DB, c.GetUint("uid"), nodeID)
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

func (h handler) machineScope(c *gin.Context) (map[uint]bool, bool) {
	visible, all, err := scope.IDs(h.deps.DB, c.GetUint("uid"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return nil, false
	}
	raw := c.Query("nodeId")
	if raw == "" {
		if all {
			return nil, true
		}
		allowed := make(map[uint]bool, len(visible))
		for _, id := range visible {
			allowed[id] = true
		}
		return allowed, true
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "节点参数不对"})
		return nil, false
	}
	ids, err := treecore.SubtreeIDs(h.deps.DB, uint(id))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个节点"})
		return nil, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "节点读取失败"})
		return nil, false
	}
	allowed := make(map[uint]bool, len(ids))
	for _, item := range ids {
		if all || scope.Allows(visible, false, item) {
			allowed[item] = true
		}
	}
	return allowed, true
}

func (h handler) ownerMap() (map[uint][]ownerView, error) {
	var rows []struct {
		NodeID uint
		UserID uint
		Name   string
		Kind   string
	}
	err := h.deps.DB.Table("tree_node_owners").
		Select("tree_node_owners.node_id, tree_node_owners.user_id, tree_node_owners.kind, base_users.name").
		Joins("join base_users on base_users.id = tree_node_owners.user_id").
		Order("tree_node_owners.kind, base_users.name").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := map[uint][]ownerView{}
	for _, row := range rows {
		out[row.NodeID] = append(out[row.NodeID], ownerView{UserID: row.UserID, Name: row.Name, Kind: row.Kind})
	}
	return out, nil
}

var errLeafHasMachines = errors.New("leaf has machines")
