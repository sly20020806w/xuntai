package task

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xuntai/internal/model"
	taskcore "xuntai/internal/task"
	"xuntai/internal/tree"
)

type Deps struct {
	DB *gorm.DB
}

func Register(r *gin.RouterGroup, deps Deps) {
	h := handler{deps: deps}
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"module": "task"})
	})
	r.GET("/scripts", h.listScripts)
	r.POST("/scripts", h.createScript)
	r.GET("/jobs", h.listJobs)
	r.POST("/jobs", h.createJob)
	r.POST("/jobs/:id/pause", h.pause)
	r.POST("/jobs/:id/resume", h.resume)
	r.POST("/jobs/:id/results", h.report)
}

type handler struct {
	deps Deps
}

type jobView struct {
	ID         uint     `json:"id"`
	Name       string   `json:"name"`
	Status     string   `json:"status"`
	BatchSize  int      `json:"batchSize"`
	NodeID     uint     `json:"nodeId"`
	NodeName   string   `json:"nodeName"`
	ScriptName string   `json:"scriptName"`
	Done       int      `json:"done"`
	Total      int      `json:"total"`
	Issued     []string `json:"issued"`
}

func (h handler) listScripts(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var rows []model.Script
	if err := h.deps.DB.Order("id").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "脚本读取失败"})
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (h handler) createScript(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Content) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要脚本名和内容"})
		return
	}
	row := model.Script{Name: strings.TrimSpace(body.Name), Content: strings.TrimSpace(body.Content)}
	if err := h.deps.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "脚本没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "name": row.Name})
}

func (h handler) listJobs(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	query := h.deps.DB.Table("task_jobs").
		Select("task_jobs.id, task_jobs.name, task_jobs.status, task_jobs.batch_size, task_jobs.tree_node_id as node_id, tree_nodes.name as node_name, task_scripts.name as script_name").
		Joins("left join tree_nodes on tree_nodes.id = task_jobs.tree_node_id").
		Joins("left join task_scripts on task_scripts.id = task_jobs.script_id").
		Order("task_jobs.id desc")
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
		query = query.Where("task_jobs.tree_node_id IN ?", ids)
	}
	var rows []struct {
		ID         uint
		Name       string
		Status     string
		BatchSize  int
		NodeID     uint
		NodeName   string
		ScriptName string
	}
	if err := query.Scan(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "任务读取失败"})
		return
	}
	out := make([]jobView, 0, len(rows))
	for _, row := range rows {
		view := jobView{
			ID: row.ID, Name: row.Name, Status: row.Status, BatchSize: row.BatchSize,
			NodeID: row.NodeID, NodeName: row.NodeName, ScriptName: row.ScriptName,
			Issued: []string{},
		}
		var results []model.JobResult
		if err := h.deps.DB.Where("job_id = ?", row.ID).Order("id").Find(&results).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "结果读取失败"})
			return
		}
		view.Total = len(results)
		for _, result := range results {
			if result.Status == "success" || result.Status == "failed" {
				view.Done++
			}
			if result.Status == "issued" {
				view.Issued = append(view.Issued, result.HostIP)
			}
		}
		out = append(out, view)
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createJob(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		Name       string   `json:"name"`
		ScriptID   uint     `json:"scriptId"`
		TreeNodeID uint     `json:"treeNodeId"`
		BatchSize  int      `json:"batchSize"`
		Hosts      []string `json:"hosts"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要任务名"})
		return
	}
	if !h.requireOps(c, body.TreeNodeID) {
		return
	}
	job, err := taskcore.Open(h.deps.DB, strings.TrimSpace(body.Name), body.ScriptID, body.TreeNodeID, body.BatchSize, body.Hosts)
	if err != nil {
		h.writeTaskErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": job.ID, "name": job.Name, "status": job.Status})
}

func (h handler) pause(c *gin.Context) {
	h.act(c, taskcore.Pause)
}

func (h handler) resume(c *gin.Context) {
	h.act(c, taskcore.Resume)
}

func (h handler) act(c *gin.Context, fn func(*gorm.DB, uint) error) {
	if !h.ready(c) {
		return
	}
	job, ok := h.findJob(c)
	if !ok || !h.requireOps(c, job.TreeNodeID) {
		return
	}
	if err := fn(h.deps.DB, job.ID); err != nil {
		h.writeTaskErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": job.ID})
}

func (h handler) report(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	job, ok := h.findJob(c)
	if !ok || !h.requireOps(c, job.TreeNodeID) {
		return
	}
	var body struct {
		HostIP string `json:"hostIp"`
		Status string `json:"status"`
		Output string `json:"output"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.HostIP) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要机器地址"})
		return
	}
	err := taskcore.Report(h.deps.DB, job.ID, strings.TrimSpace(body.HostIP), body.Status, body.Output)
	if err != nil {
		h.writeTaskErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": job.ID})
}

func (h handler) requireOps(c *gin.Context, nodeID uint) bool {
	if nodeID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要节点"})
		return false
	}
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

func (h handler) findJob(c *gin.Context) (model.Job, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	var job model.Job
	if err != nil || id <= 0 || h.deps.DB.First(&job, id).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个任务"})
		return job, false
	}
	return job, true
}

func (h handler) ready(c *gin.Context) bool {
	if h.deps.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "数据库还没有准备好"})
		return false
	}
	return true
}

func (h handler) writeTaskErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, taskcore.ErrNoMachines):
		c.JSON(http.StatusBadRequest, gin.H{"error": "这个节点下面没有机器"})
	case errors.Is(err, taskcore.ErrDuplicate):
		c.JSON(http.StatusBadRequest, gin.H{"error": "机器重复了"})
	case errors.Is(err, taskcore.ErrForeignHost):
		c.JSON(http.StatusBadRequest, gin.H{"error": "这台机器不在这个节点下"})
	case errors.Is(err, taskcore.ErrNotIssued):
		c.JSON(http.StatusBadRequest, gin.H{"error": "这台还没轮到"})
	case errors.Is(err, taskcore.ErrUnknownHost):
		c.JSON(http.StatusNotFound, gin.H{"error": "任务里没有这台机器"})
	case errors.Is(err, taskcore.ErrBadStatus):
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求不对"})
	case errors.Is(err, taskcore.ErrNotRunning):
		c.JSON(http.StatusBadRequest, gin.H{"error": "任务不在执行中"})
	case errors.Is(err, taskcore.ErrNotPaused):
		c.JSON(http.StatusBadRequest, gin.H{"error": "任务没有暂停"})
	case errors.Is(err, taskcore.ErrScript):
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个脚本"})
	case errors.Is(err, taskcore.ErrNode):
		c.JSON(http.StatusNotFound, gin.H{"error": "没有这个节点"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "任务没有写成"})
	}
}
