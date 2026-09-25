package k8s

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xuntai/internal/access"
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
		c.JSON(200, gin.H{"module": "k8s"})
	})
	r.GET("/clusters", h.listClusters)
	r.POST("/clusters", h.createCluster)
	r.GET("/clusters/:id/nodes", h.listNodes)
	r.GET("/projects", h.listProjects)
	r.POST("/projects", h.createProject)
	r.POST("/apps", h.createApp)
	r.GET("/instances", h.listInstances)
	r.POST("/instances", access.ResourceCheck(deps.DB, "POST", "/api/k8s/instances", access.VerbOperate, access.ResInstance), h.createInstance)
	r.PUT("/instances/:id", access.ResourceCheck(deps.DB, "PUT", "/api/k8s/instances/:id", access.VerbOperate, access.ResInstance), h.updateInstance)
}

type handler struct {
	deps Deps
}

func (h handler) listClusters(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var rows []model.Cluster
	if err := h.deps.DB.Order("id").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "集群读取失败"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{
			"id": row.ID, "name": row.Name, "env": row.Env, "version": row.Version,
			"connected": false,
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createCluster(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		Name    string `json:"name"`
		Env     string `json:"env"`
		Version string `json:"version"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要集群名"})
		return
	}
	if body.Env != "开发" && body.Env != "生产" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "环境只能是开发或生产"})
		return
	}
	row := model.Cluster{Name: strings.TrimSpace(body.Name), Env: body.Env, Version: strings.TrimSpace(body.Version)}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "集群没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": row.Name, "env": row.Env, "connected": false})
}

func (h handler) listNodes(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	var row model.Cluster
	if err != nil || id <= 0 || h.deps.DB.First(&row, id).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个集群"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"connected": false, "nodes": []any{}})
}

func (h handler) listProjects(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	query, err := scope.Limit(h.deps.DB, c.GetUint("uid"), h.deps.DB.Order("id"), "tree_node_id")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	var rows []model.Project
	if err := query.Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "项目读取失败"})
		return
	}
	names := h.nodeNames()
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{
			"id": row.ID, "name": row.Name, "nodeId": row.TreeNodeID, "nodeName": names[row.TreeNodeID],
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createProject(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		Name       string `json:"name"`
		TreeNodeID uint   `json:"treeNodeId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" || body.TreeNodeID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要项目名和节点"})
		return
	}
	if !h.requireOps(c, body.TreeNodeID) {
		return
	}
	row := model.Project{Name: strings.TrimSpace(body.Name), TreeNodeID: body.TreeNodeID}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "项目没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": row.Name})
}

func (h handler) createApp(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		ProjectID uint   `json:"projectId"`
		Name      string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" || body.ProjectID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要项目和应用名"})
		return
	}
	project, ok := h.findProject(c, body.ProjectID)
	if !ok || !h.requireOps(c, project.TreeNodeID) {
		return
	}
	row := model.App{ProjectID: project.ID, Name: strings.TrimSpace(body.Name)}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "应用没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": row.Name})
}

func (h handler) listInstances(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	query, err := scope.Limit(h.deps.DB, c.GetUint("uid"), h.deps.DB.Table("k8s_instances").
		Select("k8s_instances.*").
		Joins("join k8s_apps on k8s_apps.id = k8s_instances.app_id").
		Joins("join k8s_projects on k8s_projects.id = k8s_apps.project_id").
		Order("k8s_instances.id"), "k8s_projects.tree_node_id")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	var rows []model.AppInstance
	if err := query.Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "实例读取失败"})
		return
	}
	apps := map[uint]model.App{}
	var appRows []model.App
	if err := h.deps.DB.Find(&appRows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "应用读取失败"})
		return
	}
	for _, app := range appRows {
		apps[app.ID] = app
	}
	projects := map[uint]model.Project{}
	var projectRows []model.Project
	if err := h.deps.DB.Find(&projectRows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "项目读取失败"})
		return
	}
	for _, project := range projectRows {
		projects[project.ID] = project
	}
	clusters := map[uint]model.Cluster{}
	var clusterRows []model.Cluster
	if err := h.deps.DB.Find(&clusterRows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "集群读取失败"})
		return
	}
	for _, cluster := range clusterRows {
		clusters[cluster.ID] = cluster
	}
	names := h.nodeNames()
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		app := apps[row.AppID]
		project := projects[app.ProjectID]
		cluster := clusters[row.ClusterID]
		out = append(out, gin.H{
			"id": row.ID, "appId": row.AppID, "appName": app.Name,
			"nodeId": project.TreeNodeID, "nodeName": names[project.TreeNodeID],
			"clusterId": row.ClusterID, "cluster": cluster.Name, "env": cluster.Env,
			"image": row.Image, "replicas": row.Replicas,
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createInstance(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		AppID     uint   `json:"appId"`
		ClusterID uint   `json:"clusterId"`
		Image     string `json:"image"`
		Replicas  int    `json:"replicas"`
		TicketID  uint   `json:"ticketId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Image) == "" || body.AppID == 0 || body.ClusterID == 0 || body.Replicas < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要应用、集群、镜像和副本"})
		return
	}
	if !h.allowDesired(c, body.AppID, body.ClusterID, body.TicketID) {
		return
	}
	row := model.AppInstance{
		AppID: body.AppID, ClusterID: body.ClusterID,
		Image: strings.TrimSpace(body.Image), Replicas: body.Replicas,
	}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "实例没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "image": row.Image, "replicas": row.Replicas})
}

func (h handler) updateInstance(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	var row model.AppInstance
	if err != nil || id <= 0 || h.deps.DB.First(&row, id).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个实例"})
		return
	}
	var body struct {
		Image    string `json:"image"`
		Replicas int    `json:"replicas"`
		TicketID uint   `json:"ticketId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Image) == "" || body.Replicas < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要镜像和副本"})
		return
	}
	var cluster model.Cluster
	if err := h.deps.DB.First(&cluster, row.ClusterID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个集群"})
		return
	}
	if cluster.Env == "生产" {
		var app model.App
		if err := h.deps.DB.First(&app, row.AppID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "没有这个应用"})
			return
		}
		project, ok := h.findProject(c, app.ProjectID)
		if !ok || !access.Permit(c, h.deps.DB, project.TreeNodeID) {
			return
		}
		c.JSON(http.StatusConflict, gin.H{"error": "生产镜像请走生产发布或回滚剧本"})
		return
	}
	if !h.allowDesired(c, row.AppID, row.ClusterID, body.TicketID) {
		return
	}
	row.Image = strings.TrimSpace(body.Image)
	row.Replicas = body.Replicas
	if err := h.deps.DB.Model(&row).Updates(map[string]any{"image": row.Image, "replicas": row.Replicas}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "实例没有改成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "image": row.Image, "replicas": row.Replicas})
}

func (h handler) allowDesired(c *gin.Context, appID, clusterID, ticketID uint) bool {
	var app model.App
	if err := h.deps.DB.First(&app, appID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个应用"})
		return false
	}
	project, ok := h.findProject(c, app.ProjectID)
	if !ok || !access.Permit(c, h.deps.DB, project.TreeNodeID) {
		return false
	}
	var cluster model.Cluster
	if err := h.deps.DB.First(&cluster, clusterID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个集群"})
		return false
	}
	if cluster.Env != "生产" {
		return true
	}
	if ticketID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "生产变更要等工单审批通过"})
		return false
	}
	var ticket model.TicketInstance
	if err := h.deps.DB.First(&ticket, ticketID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这张工单"})
		return false
	}
	if ticket.Status != "pending_action" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "生产变更要等工单审批通过"})
		return false
	}
	if ticket.TreeNodeID != project.TreeNodeID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "工单不在这个节点上"})
		return false
	}
	return true
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

func (h handler) findProject(c *gin.Context, id uint) (model.Project, bool) {
	var project model.Project
	if err := h.deps.DB.First(&project, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个项目"})
		return project, false
	}
	return project, true
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
