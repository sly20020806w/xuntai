package monitor

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xuntai/internal/model"
	moncore "xuntai/internal/monitor"
	"xuntai/internal/tree"
)

type Deps struct {
	DB *gorm.DB
}

func Register(r *gin.RouterGroup, deps Deps) {
	h := handler{deps: deps}
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"module": "monitor"})
	})
	r.GET("/pools", h.listPools)
	r.POST("/pools", h.createPool)
	r.GET("/pools/:id/targets", h.pullTargets)
	r.GET("/jobs", h.listJobs)
	r.POST("/jobs", h.createJob)
	r.GET("/send-groups", h.listSendGroups)
	r.GET("/rules", h.listRules)
	r.POST("/rules", h.createRule)
}

type handler struct {
	deps Deps
}

func (h handler) listPools(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var rows []model.ScrapePool
	if err := h.deps.DB.Order("id").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "采集池读取失败"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{
			"id": row.ID, "name": row.Name, "remoteWrite": row.RemoteWrite, "supportAlert": row.SupportAlert,
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createPool(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		Name         string `json:"name"`
		RemoteWrite  string `json:"remoteWrite"`
		SupportAlert bool   `json:"supportAlert"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.RemoteWrite) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要池名和远端写入"})
		return
	}
	row := model.ScrapePool{
		Name: strings.TrimSpace(body.Name), RemoteWrite: strings.TrimSpace(body.RemoteWrite),
		SupportAlert: body.SupportAlert,
	}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "采集池没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": row.Name, "supportAlert": row.SupportAlert})
}

func (h handler) pullTargets(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个采集池"})
		return
	}
	file, err := moncore.Pull(h.deps.DB, uint(id))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个采集池"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "采集配置没有生成"})
		return
	}
	c.JSON(http.StatusOK, file)
}

func (h handler) listJobs(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var jobs []model.ScrapeJob
	if err := h.deps.DB.Order("id").Find(&jobs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "采集任务读取失败"})
		return
	}
	names := h.nodeNames()
	pools := h.poolNames()
	out := make([]gin.H, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, gin.H{
			"id": job.ID, "name": job.Name, "discover": job.Discover,
			"port": job.Port, "metricsPath": job.MetricsPath,
			"nodeId": job.TreeNodeID, "nodeName": names[job.TreeNodeID],
			"poolId": job.PoolID, "poolName": pools[job.PoolID],
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createJob(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		PoolID      uint   `json:"poolId"`
		TreeNodeID  uint   `json:"treeNodeId"`
		Name        string `json:"name"`
		Discover    string `json:"discover"`
		Port        int    `json:"port"`
		MetricsPath string `json:"metricsPath"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" || body.PoolID == 0 || body.TreeNodeID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要池、节点和任务名"})
		return
	}
	if body.Discover == "" {
		body.Discover = "tree"
	}
	if body.Discover != "tree" && body.Discover != "k8s" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "发现方式只能是服务树或集群"})
		return
	}
	var pool model.ScrapePool
	if err := h.deps.DB.First(&pool, body.PoolID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个采集池"})
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
	if body.Discover == "tree" && !node.IsLeaf {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只有叶子节点能按服务树发现"})
		return
	}
	if body.Discover == "tree" && (body.Port < 1 || body.Port > 65535) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要采集端口"})
		return
	}
	path := strings.TrimSpace(body.MetricsPath)
	if path == "" {
		path = "/metrics"
	}
	row := model.ScrapeJob{
		PoolID: pool.ID, TreeNodeID: node.ID, Name: strings.TrimSpace(body.Name),
		Discover: body.Discover, Port: body.Port, MetricsPath: path,
	}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "采集任务没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": row.Name, "discover": row.Discover})
}

func (h handler) listSendGroups(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var groups []model.SendGroup
	if err := h.deps.DB.Order("id").Find(&groups).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发送组读取失败"})
		return
	}
	duties := map[uint]string{}
	var dutyRows []model.DutyGroup
	if err := h.deps.DB.Find(&dutyRows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "值班组读取失败"})
		return
	}
	for _, duty := range dutyRows {
		duties[duty.ID] = duty.Name
	}
	out := make([]gin.H, 0, len(groups))
	for _, group := range groups {
		out = append(out, gin.H{
			"id": group.ID, "sendgroup_id": group.ID, "name": group.Name,
			"duty": duties[group.DutyGroupID],
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) listRules(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var rules []model.AlertRule
	if err := h.deps.DB.Order("id").Find(&rules).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "规则读取失败"})
		return
	}
	pools := h.poolNames()
	groups := map[uint]string{}
	var groupRows []model.SendGroup
	if err := h.deps.DB.Find(&groupRows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发送组读取失败"})
		return
	}
	for _, group := range groupRows {
		groups[group.ID] = group.Name
	}
	status := map[uint]string{}
	var events []model.AlertEvent
	if err := h.deps.DB.Order("id").Find(&events).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "告警状态读取失败"})
		return
	}
	for _, event := range events {
		status[event.RuleID] = event.Status
	}
	out := make([]gin.H, 0, len(rules))
	for _, rule := range rules {
		out = append(out, gin.H{
			"id": rule.ID, "name": rule.Name, "expr": rule.Expr, "level": rule.Level,
			"poolName": pools[rule.PoolID], "sendGroupName": groups[rule.SendGroupID],
			"sendgroup_id": rule.SendGroupID, "status": status[rule.ID],
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createRule(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		Name        string `json:"name"`
		Expr        string `json:"expr"`
		Level       string `json:"level"`
		PoolID      uint   `json:"poolId"`
		SendGroupID uint   `json:"sendGroupId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Expr) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要规则名和表达式"})
		return
	}
	if body.Level != "紧急" && body.Level != "警告" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "级别只能是紧急或警告"})
		return
	}
	var pool model.ScrapePool
	if err := h.deps.DB.First(&pool, body.PoolID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个采集池"})
		return
	}
	if !pool.SupportAlert {
		c.JSON(http.StatusBadRequest, gin.H{"error": "这个采集池不产生告警"})
		return
	}
	var group model.SendGroup
	if err := h.deps.DB.First(&group, body.SendGroupID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个发送组"})
		return
	}
	row := model.AlertRule{
		Name: strings.TrimSpace(body.Name), Expr: strings.TrimSpace(body.Expr), Level: body.Level,
		PoolID: pool.ID, SendGroupID: group.ID,
	}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "规则没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": row.Name, "sendgroup_id": row.SendGroupID})
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

func (h handler) poolNames() map[uint]string {
	out := map[uint]string{}
	var rows []model.ScrapePool
	if err := h.deps.DB.Find(&rows).Error; err != nil {
		return out
	}
	for _, row := range rows {
		out[row.ID] = row.Name
	}
	return out
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
