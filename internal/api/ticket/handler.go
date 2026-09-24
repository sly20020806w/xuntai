package ticket

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xuntai/internal/model"
	"xuntai/internal/tree"
)

type Deps struct {
	DB *gorm.DB
}

func Register(r *gin.RouterGroup, deps Deps) {
	h := handler{deps: deps}
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"module": "ticket"})
	})
	r.GET("/templates", h.listTemplates)
	r.GET("/instances", h.listInstances)
	r.POST("/instances", h.createInstance)
	r.POST("/instances/:id/approve", h.approve)
	r.POST("/instances/:id/reject", h.reject)
	r.POST("/instances/:id/finish", h.finish)
}

type handler struct {
	deps Deps
}

type templateView struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	FormJSON string `json:"form"`
	FlowJSON string `json:"flow"`
}

type instanceView struct {
	ID           uint   `json:"id" gorm:"column:id"`
	Title        string `json:"title" gorm:"column:title"`
	Status       string `json:"status" gorm:"column:status"`
	NodeID       uint   `json:"nodeId" gorm:"column:node_id"`
	NodeName     string `json:"nodeName" gorm:"column:node_name"`
	Applicant    string `json:"applicant" gorm:"column:applicant"`
	TemplateID   uint   `json:"templateId" gorm:"column:template_id"`
	TemplateName string `json:"templateName" gorm:"column:template_name"`
}

func (h handler) listTemplates(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var rows []model.TicketTemplate
	if err := h.deps.DB.Order("id").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "模板读取失败"})
		return
	}
	out := make([]templateView, 0, len(rows))
	for _, row := range rows {
		out = append(out, templateView{ID: row.ID, Name: row.Name, FormJSON: row.FormJSON, FlowJSON: row.FlowJSON})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) listInstances(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	query := h.deps.DB.Table("ticket_instances").
		Select("ticket_instances.id, ticket_instances.title, ticket_instances.status, ticket_instances.tree_node_id as node_id, tree_nodes.name as node_name, base_users.name as applicant, ticket_instances.template_id, ticket_templates.name as template_name").
		Joins("left join tree_nodes on tree_nodes.id = ticket_instances.tree_node_id").
		Joins("left join base_users on base_users.id = ticket_instances.applicant_id").
		Joins("left join ticket_templates on ticket_templates.id = ticket_instances.template_id").
		Order("ticket_instances.id desc")
	if raw := c.Query("nodeId"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "节点参数不对"})
			return
		}
		ids, err := tree.SubtreeIDs(h.deps.DB, uint(id))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "没有这个节点"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "节点读取失败"})
			return
		}
		query = query.Where("ticket_instances.tree_node_id IN ?", ids)
	}
	var out []instanceView
	if err := query.Scan(&out).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "工单读取失败"})
		return
	}
	if out == nil {
		out = []instanceView{}
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createInstance(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		TemplateID uint   `json:"templateId"`
		TreeNodeID uint   `json:"treeNodeId"`
		Title      string `json:"title"`
		Payload    string `json:"payload"`
		Status     string `json:"status"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Title) == "" || body.TemplateID == 0 || body.TreeNodeID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要模板、节点和标题"})
		return
	}
	var tpl model.TicketTemplate
	if err := h.deps.DB.First(&tpl, body.TemplateID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个模板"})
		return
	}
	var node model.Node
	if err := h.deps.DB.First(&node, body.TreeNodeID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个节点"})
		return
	}
	on, err := tree.OnNode(h.deps.DB, c.GetUint("uid"), node.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	if !on {
		c.JSON(http.StatusForbidden, gin.H{"error": "不在这个节点上"})
		return
	}
	payload := strings.TrimSpace(body.Payload)
	if payload == "" {
		payload = "{}"
	}
	row := model.TicketInstance{
		TemplateID:  tpl.ID,
		TreeNodeID:  node.ID,
		Title:       strings.TrimSpace(body.Title),
		Status:      "pending_approve",
		ApplicantID: c.GetUint("uid"),
		CurrentNode: "approve",
		PayloadJSON: payload,
	}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "工单没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id": row.ID, "title": row.Title, "status": row.Status, "nodeId": row.TreeNodeID,
	})
}

func (h handler) approve(c *gin.Context) {
	h.move(c, "pending_approve", "pending_action", "action", true)
}

func (h handler) reject(c *gin.Context) {
	h.move(c, "pending_approve", "reject", "", true)
}

func (h handler) finish(c *gin.Context) {
	h.move(c, "pending_action", "finished", "", false)
}

func (h handler) move(c *gin.Context, from, to, step string, blockApplicant bool) {
	if !h.ready(c) {
		return
	}
	row, ok := h.findInstance(c)
	if !ok {
		return
	}
	allowed, err := tree.CanWrite(h.deps.DB, c.GetUint("uid"), row.TreeNodeID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个节点"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "没有这个节点的运维权限"})
		return
	}
	if blockApplicant && row.ApplicantID == c.GetUint("uid") {
		c.JSON(http.StatusForbidden, gin.H{"error": "不能审批自己的工单"})
		return
	}
	if from == "pending_action" && row.Status == "pending_approve" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "还没审批通过，不能执行"})
		return
	}
	res := h.deps.DB.Model(&model.TicketInstance{}).
		Where("id = ? AND status = ?", row.ID, from).
		Updates(map[string]any{"status": to, "current_node": step})
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "工单没有改成"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "这张单不能再改"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "status": to})
}

func (h handler) ready(c *gin.Context) bool {
	if h.deps.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "数据库还没有准备好"})
		return false
	}
	return true
}

func (h handler) findInstance(c *gin.Context) (model.TicketInstance, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	var row model.TicketInstance
	if err != nil || id <= 0 || h.deps.DB.First(&row, id).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这张工单"})
		return row, false
	}
	return row, true
}
