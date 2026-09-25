package playbook

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xuntai/internal/model"
	playcore "xuntai/internal/playbook"
	"xuntai/internal/scope"
)

type Deps struct {
	DB *gorm.DB
}

func Register(r *gin.RouterGroup, deps Deps) {
	h := handler{deps: deps}
	r.GET("/playbooks", h.listBooks)
	r.GET("/playbooks/:id", h.getBook)
	r.GET("/runs", h.listRuns)
	r.POST("/runs", h.start)
	r.GET("/runs/:id", h.getRun)
	r.POST("/runs/:id/continue", h.continueRun)
	r.POST("/runs/:id/cancel", h.cancel)
	r.POST("/runs/:id/retry", h.retry)
	r.POST("/runs/:id/sync", h.sync)
}

type handler struct {
	deps Deps
}

func (h handler) listBooks(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var rows []model.Playbook
	if err := h.deps.DB.Order("id").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "剧本读取失败"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{"id": row.ID, "code": row.Code, "name": row.Name, "status": row.Status})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) getBook(c *gin.Context) {
	row, ok := h.findBook(c)
	if !ok {
		return
	}
	var steps []model.PlaybookStep
	if err := h.deps.DB.Where("playbook_id = ?", row.ID).Order("seq").Find(&steps).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "步骤读取失败"})
		return
	}
	stepOut := make([]gin.H, 0, len(steps))
	for _, step := range steps {
		stepOut = append(stepOut, gin.H{"key": step.StepKey, "kind": step.Kind, "seq": step.Seq, "onError": step.OnError})
	}
	c.JSON(http.StatusOK, gin.H{"id": row.ID, "code": row.Code, "name": row.Name, "status": row.Status, "inputSchema": row.InputSchema, "steps": stepOut})
}

func (h handler) listRuns(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	query, err := scope.Limit(h.deps.DB, c.GetUint("uid"), h.deps.DB.Order("id desc"), "tree_node_id")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return
	}
	var rows []model.Run
	if err := query.Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "执行读取失败"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, h.runView(row))
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) start(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var body struct {
		Playbook       string         `json:"playbook"`
		IdempotencyKey string         `json:"idempotencyKey"`
		Input          map[string]any `json:"input"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Playbook == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要剧本"})
		return
	}
	run, err := playcore.Start(h.deps.DB, body.Playbook, c.GetUint("uid"), body.IdempotencyKey, body.Input, "")
	if err != nil {
		h.writeErr(c, err, run.ID)
		return
	}
	c.JSON(http.StatusOK, h.runView(run))
}

func (h handler) getRun(c *gin.Context) {
	row, ok := h.findRun(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, h.runView(row))
}

func (h handler) continueRun(c *gin.Context) {
	row, ok := h.findRun(c)
	if !ok {
		return
	}
	var body struct {
		StepID        uint           `json:"stepId"`
		Version       int            `json:"version"`
		ContinueInput map[string]any `json:"continueInput"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.StepID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要步骤和版本"})
		return
	}
	if err := playcore.Continue(h.deps.DB, row.ID, body.StepID, body.Version, c.GetUint("uid"), body.ContinueInput); err != nil {
		h.writeErr(c, err, 0)
		return
	}
	_ = h.deps.DB.First(&row, row.ID).Error
	c.JSON(http.StatusOK, h.runView(row))
}

func (h handler) cancel(c *gin.Context) {
	row, ok := h.findRun(c)
	if !ok {
		return
	}
	if err := playcore.Cancel(h.deps.DB, row.ID); err != nil {
		h.writeErr(c, err, 0)
		return
	}
	_ = h.deps.DB.First(&row, row.ID).Error
	c.JSON(http.StatusOK, h.runView(row))
}

func (h handler) retry(c *gin.Context) {
	row, ok := h.findRun(c)
	if !ok {
		return
	}
	run, err := playcore.Retry(h.deps.DB, row.ID, c.GetUint("uid"))
	if err != nil {
		h.writeErr(c, err, run.ID)
		return
	}
	c.JSON(http.StatusOK, h.runView(run))
}

func (h handler) sync(c *gin.Context) {
	row, ok := h.findRun(c)
	if !ok {
		return
	}
	if err := playcore.Advance(h.deps.DB, row.ID); err != nil {
		h.writeErr(c, err, 0)
		return
	}
	_ = h.deps.DB.First(&row, row.ID).Error
	c.JSON(http.StatusOK, h.runView(row))
}

func (h handler) runView(row model.Run) gin.H {
	var book model.Playbook
	_ = h.deps.DB.First(&book, row.PlaybookID).Error
	var steps []model.RunStep
	_ = h.deps.DB.Where("run_id = ?", row.ID).Order("seq").Find(&steps).Error
	stepOut := make([]gin.H, 0, len(steps))
	var current uint
	for i, step := range steps {
		if i == row.CurrentStepIndex {
			current = step.ID
		}
		var output any
		if step.OutputJSON != "" {
			_ = json.Unmarshal([]byte(step.OutputJSON), &output)
		}
		stepOut = append(stepOut, gin.H{
			"id": step.ID, "key": step.StepKey, "kind": step.Kind, "status": step.Status,
			"error": step.Error, "output": output,
		})
	}
	return gin.H{
		"id": row.ID, "playbook": book.Code, "status": row.Status, "nodeId": row.TreeNodeID,
		"idempotencyKey": row.IdempotencyKey, "version": row.Version, "audit": row.Audit,
		"currentStepId": current, "steps": stepOut,
	}
}

func (h handler) writeErr(c *gin.Context, err error, runID uint) {
	switch {
	case errors.Is(err, playcore.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "runId": runID})
	case errors.Is(err, playcore.ErrVersion):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, playcore.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, playcore.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, playcore.ErrBadState), errors.Is(err, playcore.ErrNotPublished), errors.Is(err, playcore.ErrTerminal):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	}
}

func (h handler) findBook(c *gin.Context) (model.Playbook, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	var row model.Playbook
	if !h.ready(c) || err != nil || id <= 0 || h.deps.DB.First(&row, id).Error != nil {
		if h.deps.DB != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "没有这个剧本"})
		}
		return row, false
	}
	return row, true
}

func (h handler) findRun(c *gin.Context) (model.Run, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	var row model.Run
	if !h.ready(c) || err != nil || id <= 0 || h.deps.DB.First(&row, id).Error != nil {
		if h.deps.DB != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "没有这次执行"})
		}
		return row, false
	}
	ids, all, err := scope.IDs(h.deps.DB, c.GetUint("uid"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "权限核对失败"})
		return row, false
	}
	if !scope.Allows(ids, all, row.TreeNodeID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "没有这个节点的运维权限"})
		return row, false
	}
	return row, true
}

func (h handler) ready(c *gin.Context) bool {
	if h.deps.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "数据库还没有准备好"})
		return false
	}
	return true
}
