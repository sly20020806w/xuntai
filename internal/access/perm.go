package access

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xuntai/internal/model"
	"xuntai/internal/tree"
)

const (
	VerbView    = "view"
	VerbOperate = "operate"
	VerbApprove = "approve"
	VerbAdmin   = "admin"
)

const (
	ResTree        = "tree"
	ResModel       = "model"
	ResObject      = "object"
	ResObjectNode  = "object_node"
	ResInstance    = "instance"
	ResReleaseItem = "release_item"
	ResTicket      = "ticket"
	ResPlaybookRun = "playbook_run"
	ResTaskJob     = "task_job"
	ResDBInstance  = "db_instance"
)

const (
	msgUnmounted = "资源未挂载到服务树，无法校验归属"
	msgDenied    = "没有这个节点的运维权限"
	msgNoNode    = "没有这个节点"
	msgCheck     = "权限核对失败"
)

var (
	ErrUnmounted  = errors.New("unmounted")
	ErrDenied     = errors.New("denied")
	ErrNodeAbsent = errors.New("node absent")
)

// Route 是一条已经接入资源归属校验的写接口。
type Route struct {
	Method   string
	Path     string
	Verb     string
	Resource string
}

// Routes 在注册写接口时写入，测试用来确认动词没有散落到模块里。
var Routes []Route

func KnownVerb(verb string) bool {
	switch verb {
	case VerbView, VerbOperate, VerbApprove, VerbAdmin:
		return true
	default:
		return false
	}
}

func KnownResource(resource string) bool {
	switch resource {
	case ResTree, ResModel, ResObject, ResObjectNode, ResInstance, ResReleaseItem, ResTicket, ResPlaybookRun, ResTaskJob, ResDBInstance:
		return true
	default:
		return false
	}
}

// Allow 先放行平台管理员，再沿服务树核对运维负责人。节点为 0 视为未挂树。
func Allow(db *gorm.DB, userID, nodeID uint) error {
	admin, err := platformAdmin(db, userID)
	if err != nil {
		return err
	}
	if admin {
		return nil
	}
	if nodeID == 0 {
		return ErrUnmounted
	}
	ok, err := tree.CanWrite(db, userID, nodeID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNodeAbsent
	}
	if err != nil {
		return err
	}
	if !ok {
		return ErrDenied
	}
	return nil
}

// Deny 把归属校验的结果写成响应。
func Deny(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrUnmounted):
		c.AbortWithStatusJSON(403, gin.H{"error": msgUnmounted})
	case errors.Is(err, ErrDenied):
		c.AbortWithStatusJSON(403, gin.H{"error": msgDenied})
	case errors.Is(err, ErrNodeAbsent):
		c.AbortWithStatusJSON(404, gin.H{"error": msgNoNode})
	default:
		c.AbortWithStatusJSON(500, gin.H{"error": msgCheck})
	}
}

// Permit 给处理函数复用同一次归属判断，避免平台管理员在中间件放行后又被旧校验拦住。
func Permit(c *gin.Context, db *gorm.DB, nodeID uint) bool {
	if err := Allow(db, c.GetUint("uid"), nodeID); err != nil {
		Deny(c, err)
		return false
	}
	return true
}

// ResourceCheck 接在 JWT/Casbin 之后。verb 和 resource 由路由声明，不从请求里信任。
func ResourceCheck(db *gorm.DB, method, path, verb, resource string) gin.HandlerFunc {
	remember(method, path, verb, resource)
	return func(c *gin.Context) {
		if db == nil {
			c.AbortWithStatusJSON(503, gin.H{"error": "数据库还没有准备好"})
			return
		}
		nodeID, found, err := resolve(db, c, resource)
		if err != nil {
			Deny(c, err)
			return
		}
		if !found {
			c.Next()
			return
		}
		if !Permit(c, db, nodeID) {
			return
		}
		c.Next()
	}
}

func remember(method, path, verb, resource string) {
	for _, row := range Routes {
		if row.Method == method && row.Path == path {
			return
		}
	}
	Routes = append(Routes, Route{Method: method, Path: path, Verb: verb, Resource: resource})
}

func platformAdmin(db *gorm.DB, userID uint) (bool, error) {
	var user model.User
	if err := db.Preload("Roles").First(&user, userID).Error; err != nil {
		return false, err
	}
	for _, role := range user.Roles {
		if role.Name == "平台管理员" {
			return true, nil
		}
	}
	return false, nil
}

func resolve(db *gorm.DB, c *gin.Context, resource string) (uint, bool, error) {
	switch resource {
	case ResTicket:
		return rowNode(db, c.Param("id"), func(id uint) (uint, bool, error) {
			var row model.TicketInstance
			err := db.First(&row, id).Error
			return row.TreeNodeID, err == nil, err
		})
	case ResPlaybookRun:
		if c.Param("id") == "" {
			return inputNode(c)
		}
		return rowNode(db, c.Param("id"), func(id uint) (uint, bool, error) {
			var row model.Run
			err := db.First(&row, id).Error
			return row.TreeNodeID, err == nil, err
		})
	case ResInstance:
		if c.Param("id") == "" {
			return instanceByApp(db, c)
		}
		return rowNode(db, c.Param("id"), func(id uint) (uint, bool, error) {
			return instanceNode(db, id)
		})
	case ResModel:
		return 0, true, nil
	case ResObject:
		if c.Param("id") == "" {
			return bodyNode(c, "treeNodeId")
		}
		return rowNode(db, c.Param("id"), func(id uint) (uint, bool, error) {
			var row model.CMDBObject
			err := db.First(&row, id).Error
			return row.TreeNodeID, err == nil, err
		})
	case ResObjectNode:
		if c.Param("id") == "" {
			return bodyNode(c, "nodeId")
		}
		return rowNode(db, c.Param("id"), func(id uint) (uint, bool, error) {
			var row model.ObjectNode
			err := db.First(&row, id).Error
			return row.NodeID, err == nil, err
		})
	case ResReleaseItem:
		if strings.Contains(c.FullPath(), "/cicd/items/") && c.Param("id") != "" {
			return rowNode(db, c.Param("id"), func(id uint) (uint, bool, error) {
				var item model.DeployItem
				err := db.First(&item, id).Error
				return item.TreeNodeID, err == nil, err
			})
		}
		if c.Param("id") == "" {
			return bodyNode(c, "treeNodeId")
		}
		return rowNode(db, c.Param("id"), func(id uint) (uint, bool, error) {
			var order model.ReleaseOrder
			if err := db.First(&order, id).Error; err != nil {
				return 0, false, err
			}
			var item model.DeployItem
			err := db.First(&item, order.ItemID).Error
			return item.TreeNodeID, err == nil, err
		})
	case ResTaskJob:
		return bodyNode(c, "treeNodeId")
	case ResDBInstance:
		if c.Param("id") != "" {
			return rowNode(db, c.Param("id"), func(id uint) (uint, bool, error) {
				var row model.Instance
				err := db.First(&row, id).Error
				return row.TreeNodeID, err == nil, err
			})
		}
		return bodyNode(c, "treeNodeId")
	default:
		return 0, false, errors.New("unknown resource")
	}
}

func rowNode(db *gorm.DB, raw string, load func(uint) (uint, bool, error)) (uint, bool, error) {
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, false, nil
	}
	nodeID, found, err := load(uint(id))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil && !found {
		return 0, false, err
	}
	return nodeID, found, nil
}

func instanceNode(db *gorm.DB, id uint) (uint, bool, error) {
	var row model.AppInstance
	if err := db.First(&row, id).Error; err != nil {
		return 0, false, err
	}
	return projectNode(db, row.AppID)
}

func instanceByApp(db *gorm.DB, c *gin.Context) (uint, bool, error) {
	var body struct {
		AppID uint `json:"appId"`
	}
	if !peek(c, &body) || body.AppID == 0 {
		return 0, false, nil
	}
	return projectNode(db, body.AppID)
}

func projectNode(db *gorm.DB, appID uint) (uint, bool, error) {
	var app model.App
	if err := db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}
	var project model.Project
	if err := db.First(&project, app.ProjectID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return project.TreeNodeID, true, nil
}

func bodyNode(c *gin.Context, key string) (uint, bool, error) {
	var body map[string]any
	if !peek(c, &body) {
		return 0, false, nil
	}
	if _, ok := body[key]; !ok {
		return 0, true, nil
	}
	id, ok := asUint(body[key])
	if !ok {
		return 0, true, nil
	}
	return id, true, nil
}

func inputNode(c *gin.Context) (uint, bool, error) {
	var body struct {
		Input map[string]any `json:"input"`
	}
	if !peek(c, &body) {
		return 0, false, nil
	}
	if body.Input == nil {
		return 0, true, nil
	}
	if _, ok := body.Input["tree_node_id"]; !ok {
		return 0, true, nil
	}
	id, ok := asUint(body.Input["tree_node_id"])
	if !ok {
		return 0, true, nil
	}
	return id, true, nil
}

func peek(c *gin.Context, dest any) bool {
	if c.Request == nil || c.Request.Body == nil {
		return false
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.Request.Body = io.NopCloser(bytes.NewReader(nil))
		return false
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	if len(bytes.TrimSpace(raw)) == 0 {
		return false
	}
	return json.Unmarshal(raw, dest) == nil
}

func asUint(value any) (uint, bool) {
	switch n := value.(type) {
	case float64:
		if n <= 0 {
			return 0, false
		}
		return uint(n), true
	case int:
		if n <= 0 {
			return 0, false
		}
		return uint(n), true
	case uint:
		if n == 0 {
			return 0, false
		}
		return n, true
	case json.Number:
		i, err := n.Int64()
		if err != nil || i <= 0 {
			return 0, false
		}
		return uint(i), true
	default:
		return 0, false
	}
}
