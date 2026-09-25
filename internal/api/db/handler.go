package db

import (
	"errors"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xuntai/internal/access"
	dbcore "xuntai/internal/db"
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
		c.JSON(200, gin.H{"module": "db"})
	})
	r.GET("/instances", h.listInstances)
	r.GET("/instances/:id", h.instanceDetail)
	r.POST("/instances", access.ResourceCheck(deps.DB, "POST", "/api/db/instances", access.VerbAdmin, access.ResDBInstance), h.createInstance)
	r.GET("/backups", h.listBackups)
	r.POST("/backups", h.createBackup)
	r.GET("/restores", h.listRestores)
	r.POST("/restores", h.createRestore)
}

type handler struct {
	deps Deps
}

func (h handler) listInstances(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	query := h.deps.DB.Order("id")
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
		query = query.Where("tree_node_id IN ?", ids)
	}
	query, err := scope.Limit(h.deps.DB, c.GetUint("uid"), query, "tree_node_id")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	var rows []model.Instance
	if err := query.Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "实例读取失败"})
		return
	}
	names := h.nodeNames()
	masters := h.masterMap()
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, h.instanceJSON(row, names, masters, ""))
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) instanceDetail(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个实例"})
		return
	}
	row, ok := h.findInstance(c, uint(id))
	if !ok {
		return
	}
	ids, all, err := scope.IDs(h.deps.DB, c.GetUint("uid"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	if !scope.Allows(ids, all, row.TreeNodeID) {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个实例"})
		return
	}
	var objectName string
	if row.ObjectID != 0 {
		var object model.CMDBObject
		if err := h.deps.DB.First(&object, row.ObjectID).Error; err == nil {
			objectName = object.Name
		}
	}
	c.JSON(http.StatusOK, h.instanceJSON(row, h.nodeNames(), h.masterMap(), objectName))
}

func (h handler) createInstance(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		Name        string `json:"name"`
		TreeNodeID  uint   `json:"treeNodeId"`
		Host        string `json:"host"`
		Port        int    `json:"port"`
		Version     string `json:"version"`
		Role        string `json:"role"`
		MasterID    uint   `json:"masterId"`
		Password    string `json:"password"`
		Env         string `json:"env"`
		LoginUser   string `json:"loginUser"`
		MonitorAddr string `json:"monitorAddr"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要实例名和节点"})
		return
	}
	if body.TreeNodeID == 0 {
		access.Deny(c, access.ErrUnmounted)
		return
	}
	if strings.TrimSpace(body.Password) != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不保存数据库口令"})
		return
	}
	node, ok := h.findNode(c, body.TreeNodeID)
	if !ok || !access.Permit(c, h.deps.DB, node.ID) {
		return
	}
	if !node.IsLeaf {
		c.JSON(http.StatusBadRequest, gin.H{"error": "数据库只能挂在叶子上"})
		return
	}
	host := strings.TrimSpace(body.Host)
	if host == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要机器地址"})
		return
	}
	port := body.Port
	if port == 0 {
		port = 3306
	}
	if port < 1 || port > 65535 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "端口要在 1 到 65535"})
		return
	}
	version := strings.TrimSpace(body.Version)
	if version == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要版本"})
		return
	}
	role := strings.TrimSpace(body.Role)
	if role != "主" && role != "从" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "角色只能是主或从"})
		return
	}
	env := strings.TrimSpace(body.Env)
	if env == "" {
		env = "生产"
	}
	if env != "开发" && env != "测试" && env != "生产" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "环境只能是开发、测试或生产"})
		return
	}
	loginUser := strings.TrimSpace(body.LoginUser)
	if loginUser != "" && !loginNameRe.MatchString(loginUser) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "连接账号只能是字母开头的名字"})
		return
	}
	monitorAddr := strings.TrimSpace(body.MonitorAddr)
	if !monitorOK(monitorAddr) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "采集地址要是主机和端口"})
		return
	}
	if !h.hostOnLeaf(c, node.ID, host) {
		return
	}
	var taken model.Instance
	if err := h.deps.DB.Where("host = ? AND port = ?", host, port).Limit(1).Find(&taken).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "实例读取失败"})
		return
	}
	if taken.ID != 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "这台机器的端口已经登记过"})
		return
	}
	masterID := uint(0)
	if role == "主" {
		if body.MasterID != 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "主库不能再指向别的库"})
			return
		}
		var masters int64
		if err := h.deps.DB.Model(&model.Instance{}).Where("tree_node_id = ? AND role = ?", node.ID, "主").Count(&masters).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "实例读取失败"})
			return
		}
		if masters > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "这个叶子上已经有主库"})
			return
		}
	} else {
		var master model.Instance
		if body.MasterID == 0 || h.deps.DB.First(&master, body.MasterID).Error != nil || master.Role != "主" || master.TreeNodeID != node.ID {
			c.JSON(http.StatusBadRequest, gin.H{"error": "从库要指向同一叶子上的主库"})
			return
		}
		masterID = master.ID
	}
	row := model.Instance{
		Name: strings.TrimSpace(body.Name), TreeNodeID: node.ID, Host: host, Port: port,
		Version: version, Role: role, MasterID: masterID, Env: env,
		LoginUser: loginUser, MonitorAddr: monitorAddr,
	}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "实例没有建成"})
		return
	}
	if err := dbcore.Ensure(h.deps.DB, &row); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "实例没有挂到对象上"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id": row.ID, "name": row.Name, "role": row.Role, "env": row.Env,
		"objectId": row.ObjectID, "running": false,
	})
}

func (h handler) listBackups(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	query := h.deps.DB.Model(&model.Backup{}).Order("id")
	if raw := c.Query("instanceId"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "实例参数不对"})
			return
		}
		query = query.Where("instance_id = ?", id)
	}
	ids, all, err := scope.IDs(h.deps.DB, c.GetUint("uid"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	if !all {
		if len(ids) == 0 {
			query = query.Where("1 = 0")
		} else {
			query = query.Where("instance_id IN (?)", h.deps.DB.Model(&model.Instance{}).Select("id").Where("tree_node_id IN ?", ids))
		}
	}
	var rows []model.Backup
	if err := query.Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "备份读取失败"})
		return
	}
	names := h.instanceNames()
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{
			"id": row.ID, "instanceId": row.InstanceID, "instanceName": names[row.InstanceID],
			"kind": row.Kind, "keep": row.Keep, "tool": "xtrabackup", "status": row.Status, "running": false,
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createBackup(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		InstanceID uint   `json:"instanceId"`
		Kind       string `json:"kind"`
		Tool       string `json:"tool"`
		Keep       int    `json:"keep"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.InstanceID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要实例"})
		return
	}
	tool := strings.TrimSpace(body.Tool)
	if tool != "" && tool != "xtrabackup" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "MySQL 只登记物理备份"})
		return
	}
	kind := strings.TrimSpace(body.Kind)
	if kind != "全量" && kind != "增量" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "MySQL 只登记物理备份"})
		return
	}
	if body.Keep < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "保留份数至少是 1"})
		return
	}
	keep := body.Keep
	if keep == 0 {
		keep = 7
	}
	instance, ok := h.findInstance(c, body.InstanceID)
	if !ok || !h.requireOps(c, instance.TreeNodeID) {
		return
	}
	if kind == "增量" {
		var fulls int64
		if err := h.deps.DB.Model(&model.Backup{}).Where("instance_id = ? AND kind = ?", instance.ID, "全量").Count(&fulls).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "备份读取失败"})
			return
		}
		if fulls == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "增量备份要先有一次全量"})
			return
		}
	}
	row := model.Backup{InstanceID: instance.ID, Kind: kind, Keep: keep, Status: "已登记"}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "备份没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "kind": row.Kind, "keep": row.Keep, "tool": "xtrabackup", "status": row.Status, "running": false})
}

func (h handler) listRestores(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	query := h.deps.DB.Model(&model.Restore{}).Order("id desc")
	ids, all, err := scope.IDs(h.deps.DB, c.GetUint("uid"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	if !all {
		if len(ids) == 0 {
			query = query.Where("1 = 0")
		} else {
			query = query.Where("instance_id IN (?)", h.deps.DB.Model(&model.Instance{}).Select("id").Where("tree_node_id IN ?", ids))
		}
	}
	var rows []model.Restore
	if err := query.Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "还原读取失败"})
		return
	}
	names := h.instanceNames()
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{
			"id": row.ID, "backupId": row.BackupID, "instanceId": row.InstanceID,
			"instanceName": names[row.InstanceID], "ticketId": row.TicketID,
			"status": row.Status, "running": false,
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createRestore(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		BackupID   uint `json:"backupId"`
		InstanceID uint `json:"instanceId"`
		TicketID   uint `json:"ticketId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.BackupID == 0 || body.InstanceID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要备份和实例"})
		return
	}
	var backup model.Backup
	if err := h.deps.DB.First(&backup, body.BackupID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个备份"})
		return
	}
	source, ok := h.findInstance(c, backup.InstanceID)
	if !ok {
		return
	}
	target, ok := h.findInstance(c, body.InstanceID)
	if !ok || !h.requireOps(c, target.TreeNodeID) {
		return
	}
	if source.TreeNodeID != target.TreeNodeID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "备份和还原要在同一个节点上"})
		return
	}
	if !h.allowTicket(c, target.TreeNodeID, body.TicketID) {
		return
	}
	row := model.Restore{BackupID: backup.ID, InstanceID: target.ID, TicketID: body.TicketID, Status: "已登记"}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "还原没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "status": row.Status, "running": false})
}

func (h handler) allowTicket(c *gin.Context, nodeID, ticketID uint) bool {
	if ticketID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "还原要等工单审批通过"})
		return false
	}
	var ticket model.TicketInstance
	if err := h.deps.DB.First(&ticket, ticketID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这张工单"})
		return false
	}
	if ticket.Status != "pending_action" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "还原要等工单审批通过"})
		return false
	}
	if ticket.TreeNodeID != nodeID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "工单不在这个节点上"})
		return false
	}
	return true
}

func (h handler) hostOnLeaf(c *gin.Context, nodeID uint, host string) bool {
	var count int64
	err := h.deps.DB.Table("tree_machines").
		Joins("JOIN tree_node_machines ON tree_node_machines.machine_id = tree_machines.id").
		Where("tree_node_machines.node_id = ? AND tree_machines.ip = ?", nodeID, host).
		Count(&count).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "机器读取失败"})
		return false
	}
	if count == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只有这个叶子上的机器能登记"})
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

func (h handler) findNode(c *gin.Context, id uint) (model.Node, bool) {
	var node model.Node
	if err := h.deps.DB.First(&node, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个节点"})
		return node, false
	}
	return node, true
}

func (h handler) findInstance(c *gin.Context, id uint) (model.Instance, bool) {
	var row model.Instance
	if err := h.deps.DB.First(&row, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个实例"})
		return row, false
	}
	return row, true
}

func (h handler) instanceJSON(row model.Instance, names, masters map[uint]string, objectName string) gin.H {
	env := strings.TrimSpace(row.Env)
	if env == "" {
		env = "生产"
	}
	body := gin.H{
		"id": row.ID, "name": row.Name, "nodeId": row.TreeNodeID, "nodeName": names[row.TreeNodeID],
		"host": row.Host, "port": row.Port, "version": row.Version, "role": row.Role,
		"masterId": row.MasterID, "masterName": masters[row.MasterID],
		"env": env, "loginUser": row.LoginUser, "monitorAddr": row.MonitorAddr,
		"objectId": row.ObjectID, "running": false,
	}
	if objectName != "" {
		body["objectName"] = objectName
	}
	return body
}

func (h handler) masterMap() map[uint]string {
	out := map[uint]string{}
	var heads []model.Instance
	if err := h.deps.DB.Where("role = ?", "主").Find(&heads).Error; err != nil {
		return out
	}
	for _, row := range heads {
		out[row.ID] = row.Name
	}
	return out
}

func monitorOK(addr string) bool {
	if addr == "" {
		return true
	}
	if strings.Contains(addr, "@") || strings.Contains(addr, "://") || strings.ContainsAny(addr, " '\"") {
		return false
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

var loginNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

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

func (h handler) instanceNames() map[uint]string {
	out := map[uint]string{}
	var rows []model.Instance
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
