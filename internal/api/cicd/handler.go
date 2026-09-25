package cicd

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	cicdcore "xuntai/internal/cicd"
	"xuntai/internal/model"
	"xuntai/internal/scope"
	"xuntai/internal/tree"
)

type Deps struct {
	DB *gorm.DB
}

func Register(r *gin.RouterGroup, deps Deps) {
	h := handler{deps: deps}
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"module": "cicd"})
	})
	r.GET("/items", h.listItems)
	r.POST("/items", h.createItem)
	r.GET("/orders", h.listOrders)
	r.POST("/orders", h.createOrder)
	r.POST("/orders/:id/confirm", h.confirm)
}

type handler struct {
	deps Deps
}

func (h handler) listItems(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	query, err := scope.Limit(h.deps.DB, c.GetUint("uid"), h.deps.DB.Order("id"), "tree_node_id")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	var rows []model.DeployItem
	if err := query.Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发布项读取失败"})
		return
	}
	names := h.nodeNames()
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{
			"id": row.ID, "name": row.Name, "nodeId": row.TreeNodeID,
			"nodeName": names[row.TreeNodeID], "repo": row.Repo, "imageName": row.ImageName,
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createItem(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		Name       string `json:"name"`
		TreeNodeID uint   `json:"treeNodeId"`
		Repo       string `json:"repo"`
		ImageName  string `json:"imageName"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" || body.TreeNodeID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要发布项名和节点"})
		return
	}
	var node model.Node
	if err := h.deps.DB.First(&node, body.TreeNodeID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个节点"})
		return
	}
	if !h.requireOps(c, node.ID) {
		return
	}
	if !node.IsLeaf {
		c.JSON(http.StatusBadRequest, gin.H{"error": "发布项只能挂在叶子上"})
		return
	}
	image := strings.TrimSpace(body.ImageName)
	if image == "" {
		image = strings.TrimSpace(body.Name)
	}
	row := model.DeployItem{
		Name: strings.TrimSpace(body.Name), TreeNodeID: node.ID,
		Repo: strings.TrimSpace(body.Repo), ImageName: image,
	}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发布项没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": row.Name})
}

func (h handler) listOrders(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	query, err := scope.Limit(h.deps.DB, c.GetUint("uid"), h.deps.DB.Table("cicd_orders").
		Select("cicd_orders.*").
		Joins("join cicd_deploy_items on cicd_deploy_items.id = cicd_orders.item_id").
		Order("cicd_orders.id desc"), "cicd_deploy_items.tree_node_id")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	var orders []model.ReleaseOrder
	if err := query.Find(&orders).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发布单读取失败"})
		return
	}
	items := map[uint]model.DeployItem{}
	var itemRows []model.DeployItem
	if err := h.deps.DB.Find(&itemRows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发布项读取失败"})
		return
	}
	for _, item := range itemRows {
		items[item.ID] = item
	}
	names := h.nodeNames()
	out := make([]gin.H, 0, len(orders))
	for _, order := range orders {
		item := items[order.ItemID]
		var stages []model.ReleaseStage
		if err := h.deps.DB.Where("order_id = ?", order.ID).Order("seq").Find(&stages).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "阶段读取失败"})
			return
		}
		stageOut := make([]gin.H, 0, len(stages))
		for _, stage := range stages {
			stageOut = append(stageOut, gin.H{"name": stage.Name, "status": stage.Status, "seq": stage.Seq})
		}
		out = append(out, gin.H{
			"id": order.ID, "itemName": item.Name, "nodeId": item.TreeNodeID, "nodeName": names[item.TreeNodeID],
			"tag": order.Tag, "env": order.Env, "status": order.Status, "stages": stageOut,
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createOrder(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		ItemID   uint   `json:"itemId"`
		Tag      string `json:"tag"`
		Env      string `json:"env"`
		TicketID uint   `json:"ticketId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Tag) == "" || body.ItemID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要发布项和标签"})
		return
	}
	var item model.DeployItem
	if err := h.deps.DB.First(&item, body.ItemID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个发布项"})
		return
	}
	if !h.requireOps(c, item.TreeNodeID) {
		return
	}
	order, err := cicdcore.Open(h.deps.DB, item.ID, strings.TrimSpace(body.Tag), body.Env, body.TicketID)
	if err != nil {
		h.writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": order.ID, "status": order.Status, "env": order.Env, "tag": order.Tag})
}

func (h handler) confirm(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	var order model.ReleaseOrder
	if err != nil || id <= 0 || h.deps.DB.First(&order, id).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这张发布单"})
		return
	}
	var item model.DeployItem
	if err := h.deps.DB.First(&item, order.ItemID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个发布项"})
		return
	}
	if !h.requireOps(c, item.TreeNodeID) {
		return
	}
	if err := cicdcore.Confirm(h.deps.DB, order.ID); err != nil {
		h.writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": order.ID})
}

func (h handler) requireOps(c *gin.Context, nodeID uint) bool {
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

func (h handler) nodeNames() map[uint]string {
	out := map[uint]string{}
	var rows []model.Node
	if err := h.deps.DB.Find(&rows).Error; err != nil {
		return out
	}
	for _, row := range rows {
		out[row.ID] = row.Name
	}
	return out
}

func (h handler) ready(c *gin.Context) bool {
	if h.deps.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "数据库还没有准备好"})
		return false
	}
	return true
}

func (h handler) writeErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, cicdcore.ErrNeedTicket), errors.Is(err, cicdcore.ErrTicketState):
		c.JSON(http.StatusBadRequest, gin.H{"error": "生产发布要等工单审批通过"})
	case errors.Is(err, cicdcore.ErrTicketNode):
		c.JSON(http.StatusBadRequest, gin.H{"error": "工单不在这个节点上"})
	case errors.Is(err, cicdcore.ErrNoStage):
		c.JSON(http.StatusBadRequest, gin.H{"error": "这张单没有待确认的阶段"})
	case errors.Is(err, cicdcore.ErrProdStage):
		c.JSON(http.StatusConflict, gin.H{"error": "生产镜像请走生产发布或回滚剧本"})
	case errors.Is(err, cicdcore.ErrNoInstance):
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有对应的实例"})
	case errors.Is(err, cicdcore.ErrBadEnv):
		c.JSON(http.StatusBadRequest, gin.H{"error": "环境只能是开发或生产"})
	case errors.Is(err, cicdcore.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "没有找到发布需要的记录"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发布没有写成"})
	}
}
