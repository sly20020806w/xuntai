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
	"xuntai/internal/scope"
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
	r.POST("/send-groups", h.createSendGroup)
	r.PUT("/send-groups/:id", h.updateSendGroup)
	r.GET("/rules", h.listRules)
	r.POST("/rules", h.createRule)
	r.PUT("/rules/:id", h.updateRule)
	r.POST("/alerts/webhook", h.webhook)
	r.POST("/alerts/assign", h.assign)
	r.POST("/alerts/mute", h.mute)
	r.POST("/alerts/escalate", h.escalate)
	r.GET("/alerts/actions", h.listActions)
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
	query, err := scope.Limit(h.deps.DB, c.GetUint("uid"), h.deps.DB.Order("id"), "tree_node_id")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	var jobs []model.ScrapeJob
	if err := query.Find(&jobs).Error; err != nil {
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
	if body.Discover != "tree" && body.Discover != "k8s" && body.Discover != "db" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "发现方式只能是服务树、集群或数据库实例"})
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
	if (body.Discover == "tree" || body.Discover == "db") && !node.IsLeaf {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只有叶子节点能按服务树发现"})
		return
	}
	if body.Discover == "tree" && (body.Port < 1 || body.Port > 65535) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要采集端口"})
		return
	}
	if body.Discover == "db" && body.Port != 0 && (body.Port < 1 || body.Port > 65535) {
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
	query, err := scope.Limit(h.deps.DB, c.GetUint("uid"), h.deps.DB.Order("id"), "tree_node_id")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	var groups []model.SendGroup
	if err := query.Find(&groups).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发送组读取失败"})
		return
	}
	ids, all, err := scope.IDs(h.deps.DB, c.GetUint("uid"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
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
	names := h.nodeNames()
	objects := h.objectNames()
	out := make([]gin.H, 0, len(groups))
	for _, group := range groups {
		if !h.keepBinding(ids, all, group.AppID, group.ObjectID) {
			continue
		}
		out = append(out, gin.H{
			"id": group.ID, "sendgroup_id": group.ID, "name": group.Name,
			"duty":   duties[group.DutyGroupID],
			"nodeId": group.TreeNodeID, "nodeName": names[group.TreeNodeID],
			"objectId": group.ObjectID, "objectName": objects[group.ObjectID],
			"appId": group.AppID,
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createSendGroup(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	body, ok := h.bindGroup(c)
	if !ok {
		return
	}
	if !h.requireOps(c, body.TreeNodeID) {
		return
	}
	if !h.place(c, body.TreeNodeID, body.AppID, body.ObjectID) {
		return
	}
	row := model.SendGroup{
		Name: strings.TrimSpace(body.Name), TreeNodeID: body.TreeNodeID,
		AppID: body.AppID, ObjectID: body.ObjectID,
	}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发送组没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": row.Name, "nodeId": row.TreeNodeID, "objectId": row.ObjectID})
}

func (h handler) updateSendGroup(c *gin.Context) {
	row, ok := h.findGroup(c)
	if !ok || !h.requireOps(c, row.TreeNodeID) {
		return
	}
	body, ok := h.bindGroup(c)
	if !ok || !h.requireOps(c, body.TreeNodeID) {
		return
	}
	if !h.place(c, body.TreeNodeID, body.AppID, body.ObjectID) {
		return
	}
	err := h.deps.DB.Model(&row).Updates(map[string]any{
		"name": strings.TrimSpace(body.Name), "tree_node_id": body.TreeNodeID,
		"app_id": body.AppID, "object_id": body.ObjectID,
	}).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发送组没有改成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": strings.TrimSpace(body.Name), "nodeId": body.TreeNodeID, "objectId": body.ObjectID})
}

func (h handler) listRules(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	query, err := scope.Limit(h.deps.DB, c.GetUint("uid"), h.deps.DB.Order("id"), "tree_node_id")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	var rules []model.AlertRule
	if err := query.Find(&rules).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "规则读取失败"})
		return
	}
	ids, all, err := scope.IDs(h.deps.DB, c.GetUint("uid"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	names := h.nodeNames()
	pools := h.poolNames()
	objects := h.objectNames()
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
		if !h.keepBinding(ids, all, rule.AppID, rule.ObjectID) {
			continue
		}
		out = append(out, gin.H{
			"id": rule.ID, "name": rule.Name, "expr": rule.Expr, "level": rule.Level,
			"poolName": pools[rule.PoolID], "sendGroupName": groups[rule.SendGroupID],
			"sendgroup_id": rule.SendGroupID, "status": status[rule.ID],
			"nodeId": rule.TreeNodeID, "nodeName": names[rule.TreeNodeID],
			"objectId": rule.ObjectID, "objectName": objects[rule.ObjectID],
			"appId": rule.AppID,
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
		TreeNodeID  uint   `json:"treeNodeId"`
		AppID       uint   `json:"appId"`
		ObjectID    uint   `json:"objectId"`
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
	if body.TreeNodeID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要节点"})
		return
	}
	if !h.requireOps(c, body.TreeNodeID) {
		return
	}
	if !h.place(c, body.TreeNodeID, body.AppID, body.ObjectID) {
		return
	}
	row := model.AlertRule{
		Name: strings.TrimSpace(body.Name), Expr: strings.TrimSpace(body.Expr), Level: body.Level,
		PoolID: pool.ID, SendGroupID: group.ID, TreeNodeID: body.TreeNodeID,
		AppID: body.AppID, ObjectID: body.ObjectID,
	}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "规则没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": row.Name, "sendgroup_id": row.SendGroupID})
}

func (h handler) updateRule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	var row model.AlertRule
	if err != nil || id <= 0 || h.deps.DB.First(&row, id).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这条规则"})
		return
	}
	if !h.requireOps(c, row.TreeNodeID) {
		return
	}
	body, pool, group, ok := h.readRule(c)
	if !ok || !h.requireOps(c, body.TreeNodeID) || !h.place(c, body.TreeNodeID, body.AppID, body.ObjectID) {
		return
	}
	err = h.deps.DB.Model(&row).Updates(map[string]any{
		"name": strings.TrimSpace(body.Name), "expr": strings.TrimSpace(body.Expr), "level": body.Level,
		"pool_id": pool.ID, "send_group_id": group.ID, "tree_node_id": body.TreeNodeID,
		"app_id": body.AppID, "object_id": body.ObjectID,
	}).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "规则没有改成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": strings.TrimSpace(body.Name), "objectId": body.ObjectID, "appId": body.AppID})
}

func (h handler) webhook(c *gin.Context) {
	body, nodeID, owners, ok := h.openAlert(c)
	if !ok {
		return
	}
	out := make([]gin.H, 0, len(owners))
	for _, owner := range owners {
		out = append(out, gin.H{"id": owner.ID, "name": owner.Name})
	}
	c.JSON(http.StatusOK, gin.H{
		"fingerprint": body.Fingerprint, "nodeId": nodeID, "objectId": body.ObjectID, "owners": out,
	})
}

func (h handler) assign(c *gin.Context)   { h.record(c, "assign") }
func (h handler) mute(c *gin.Context)     { h.record(c, "mute") }
func (h handler) escalate(c *gin.Context) { h.record(c, "escalate") }

func (h handler) record(c *gin.Context, action string) {
	body, nodeID, owners, ok := h.openAlert(c)
	if !ok {
		return
	}
	owner := owners[0]
	if action == "escalate" && len(owners) > 1 {
		owner = owners[1]
	}
	if action == "assign" && body.OwnerID != 0 {
		found := false
		for _, item := range owners {
			if item.ID == body.OwnerID {
				owner = item
				found = true
			}
		}
		if !found {
			c.JSON(http.StatusBadRequest, gin.H{"error": "这个人不是这条链上的运维负责人"})
			return
		}
	}
	row := model.AlertAction{
		Fingerprint: body.Fingerprint, TreeNodeID: nodeID, ObjectID: body.ObjectID,
		Action: action, ActorID: c.GetUint("uid"), OwnerID: owner.ID,
	}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "处理没有记上"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "action": row.Action, "ownerId": row.OwnerID, "objectId": row.ObjectID})
}

func (h handler) listActions(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	query, err := scope.Limit(h.deps.DB, c.GetUint("uid"), h.deps.DB.Order("id"), "tree_node_id")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	if raw := c.Query("objectId"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "对象编号不对"})
			return
		}
		query = query.Where("object_id = ?", id)
	}
	if raw := c.Query("ownerId"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "负责人编号不对"})
			return
		}
		query = query.Where("owner_id = ?", id)
	}
	var rows []model.AlertAction
	if err := query.Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "处理记录读取失败"})
		return
	}
	names := h.nodeNames()
	objects := h.objectNames()
	users := h.userNames()
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{
			"id": row.ID, "fingerprint": row.Fingerprint, "action": row.Action,
			"nodeId": row.TreeNodeID, "nodeName": names[row.TreeNodeID],
			"objectId": row.ObjectID, "objectName": objects[row.ObjectID],
			"ownerId": row.OwnerID, "ownerName": users[row.OwnerID],
			"actorId": row.ActorID, "actorName": users[row.ActorID],
		})
	}
	c.JSON(http.StatusOK, out)
}

type ruleBody struct {
	Name        string `json:"name"`
	Expr        string `json:"expr"`
	Level       string `json:"level"`
	PoolID      uint   `json:"poolId"`
	SendGroupID uint   `json:"sendGroupId"`
	TreeNodeID  uint   `json:"treeNodeId"`
	AppID       uint   `json:"appId"`
	ObjectID    uint   `json:"objectId"`
}

type groupBody struct {
	Name       string `json:"name"`
	TreeNodeID uint   `json:"treeNodeId"`
	AppID      uint   `json:"appId"`
	ObjectID   uint   `json:"objectId"`
}

type alertBody struct {
	Fingerprint string `json:"fingerprint"`
	NodeID      uint   `json:"nodeId"`
	ObjectID    uint   `json:"objectId"`
	OwnerID     uint   `json:"ownerId"`
}

func (h handler) readRule(c *gin.Context) (ruleBody, model.ScrapePool, model.SendGroup, bool) {
	var body ruleBody
	var pool model.ScrapePool
	var group model.SendGroup
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Expr) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要规则名和表达式"})
		return body, pool, group, false
	}
	if body.Level != "紧急" && body.Level != "警告" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "级别只能是紧急或警告"})
		return body, pool, group, false
	}
	if err := h.deps.DB.First(&pool, body.PoolID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个采集池"})
		return body, pool, group, false
	}
	if !pool.SupportAlert {
		c.JSON(http.StatusBadRequest, gin.H{"error": "这个采集池不产生告警"})
		return body, pool, group, false
	}
	if err := h.deps.DB.First(&group, body.SendGroupID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个发送组"})
		return body, pool, group, false
	}
	if body.TreeNodeID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要节点"})
		return body, pool, group, false
	}
	return body, pool, group, true
}

func (h handler) bindGroup(c *gin.Context) (groupBody, bool) {
	var body groupBody
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" || body.TreeNodeID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要发送组名和节点"})
		return body, false
	}
	return body, true
}

func (h handler) findGroup(c *gin.Context) (model.SendGroup, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	var row model.SendGroup
	if !h.ready(c) || err != nil || id <= 0 || h.deps.DB.First(&row, id).Error != nil {
		if h.deps.DB != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "没有这个发送组"})
		}
		return row, false
	}
	return row, true
}

func (h handler) openAlert(c *gin.Context) (alertBody, uint, []model.User, bool) {
	var body alertBody
	if !h.ready(c) {
		return body, 0, nil, false
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Fingerprint) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要告警指纹"})
		return body, 0, nil, false
	}
	nodeID, err := moncore.Locate(h.deps.DB, body.NodeID, body.ObjectID)
	if err != nil {
		h.writePlace(c, err)
		return body, 0, nil, false
	}
	if !h.requireOps(c, nodeID) {
		return body, 0, nil, false
	}
	owners, err := tree.OpsOwners(h.deps.DB, nodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "负责人查找失败"})
		return body, 0, nil, false
	}
	if len(owners) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有运维负责人"})
		return body, 0, nil, false
	}
	return body, nodeID, owners, true
}

func (h handler) place(c *gin.Context, nodeID, appID, objectID uint) bool {
	if err := moncore.Place(h.deps.DB, nodeID, appID, objectID); err != nil {
		h.writePlace(c, err)
		return false
	}
	return true
}

func (h handler) writePlace(c *gin.Context, err error) {
	switch {
	case errors.Is(err, moncore.ErrNoObject):
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个对象"})
	case errors.Is(err, moncore.ErrNoApp):
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个应用"})
	case errors.Is(err, moncore.ErrObjectTree):
		c.JSON(http.StatusBadRequest, gin.H{"error": "对象没有挂到服务树"})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个节点"})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	}
}

func (h handler) keepBinding(ids []uint, all bool, appID, objectID uint) bool {
	if objectID != 0 {
		var object model.CMDBObject
		if err := h.deps.DB.First(&object, objectID).Error; err != nil {
			return false
		}
		if !scope.Allows(ids, all, object.TreeNodeID) {
			var links []model.ObjectNode
			if err := h.deps.DB.Where("object_id = ?", objectID).Find(&links).Error; err != nil {
				return false
			}
			hit := false
			for _, link := range links {
				if scope.Allows(ids, all, link.NodeID) {
					hit = true
				}
			}
			if !hit {
				return false
			}
		}
	}
	if appID != 0 {
		var app model.App
		if err := h.deps.DB.First(&app, appID).Error; err != nil {
			return false
		}
		var project model.Project
		if err := h.deps.DB.First(&project, app.ProjectID).Error; err != nil || !scope.Allows(ids, all, project.TreeNodeID) {
			return false
		}
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

func (h handler) objectNames() map[uint]string {
	out := map[uint]string{}
	var rows []model.CMDBObject
	if err := h.deps.DB.Find(&rows).Error; err != nil {
		return out
	}
	for _, row := range rows {
		out[row.ID] = row.Name
	}
	return out
}

func (h handler) userNames() map[uint]string {
	out := map[uint]string{}
	var rows []model.User
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
