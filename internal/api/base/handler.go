package base

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"xuntai/internal/access"
	"xuntai/internal/auth"
	"xuntai/internal/model"
)

type Deps struct {
	DB     *gorm.DB
	Secret string
	Gate   *access.Gate
}

func Register(r *gin.RouterGroup, deps Deps) {
	h := handler{deps: deps}
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"module": "base"})
	})
	r.POST("/login", h.login)
	r.GET("/me", h.me)
	r.GET("/menus", h.myMenus)
	r.GET("/users", h.listUsers)
	r.POST("/users", h.createUser)
	r.GET("/roles", h.listRoles)
	r.POST("/roles", h.createRole)
	r.PUT("/roles/:id/menus", h.bindMenus)
	r.PUT("/roles/:id/apis", h.bindAPIs)
	r.GET("/apis", h.listAPIs)
	r.POST("/menus", h.createMenu)
}

type handler struct {
	deps Deps
}

type menuView struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (h handler) login(c *gin.Context) {
	if h.deps.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "数据库还没有准备好"})
		return
	}
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不对"})
		return
	}
	var user model.User
	err := h.deps.DB.Preload("Roles").Where("name = ?", body.Name).First(&user).Error
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)) != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "账号或密码不对"})
		return
	}
	roles := roleNames(user.Roles)
	token, err := auth.Sign(h.deps.Secret, user.ID, user.Name, roles)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "登录状态签发失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "name": user.Name, "roles": roles})
}

func (h handler) me(c *gin.Context) {
	user, ok := h.loadUser(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"name":  user.Name,
		"roles": roleNames(user.Roles),
		"menus": menusOf(user),
	})
}

func (h handler) myMenus(c *gin.Context) {
	user, ok := h.loadUser(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, menusOf(user))
}

func (h handler) listUsers(c *gin.Context) {
	var users []model.User
	if err := h.deps.DB.Preload("Roles.Menus").Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "用户列表读取失败"})
		return
	}
	out := make([]gin.H, 0, len(users))
	for _, user := range users {
		out = append(out, gin.H{
			"id":    user.ID,
			"name":  user.Name,
			"roles": roleNames(user.Roles),
			"menus": menusOf(user),
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createUser(c *gin.Context) {
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		RoleIDs  []uint `json:"roleIds"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Name == "" || body.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要姓名和密码"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码处理失败"})
		return
	}
	user := model.User{Name: body.Name, PasswordHash: string(hash)}
	if err := h.deps.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户没有建成"})
		return
	}
	if len(body.RoleIDs) > 0 {
		var roles []model.Role
		if err := h.deps.DB.Find(&roles, body.RoleIDs).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "角色读取失败"})
			return
		}
		if err := h.deps.DB.Model(&user).Association("Roles").Replace(roles); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "角色绑定失败"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": user.ID, "name": user.Name})
}

func (h handler) listRoles(c *gin.Context) {
	var roles []model.Role
	if err := h.deps.DB.Preload("Menus").Preload("APIs").Find(&roles).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "角色列表读取失败"})
		return
	}
	out := make([]gin.H, 0, len(roles))
	for _, role := range roles {
		out = append(out, gin.H{
			"id":    role.ID,
			"name":  role.Name,
			"menus": menusOfRole(role),
			"apis":  role.APIs,
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h handler) createRole(c *gin.Context) {
	var body struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要角色名"})
		return
	}
	role := model.Role{Name: body.Name}
	if err := h.deps.DB.Create(&role).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "角色没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": role.ID, "name": role.Name})
}

func (h handler) bindMenus(c *gin.Context) {
	role, ok := h.findRole(c)
	if !ok {
		return
	}
	var body struct {
		MenuIDs []uint `json:"menuIds"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不对"})
		return
	}
	var menus []model.Menu
	if len(body.MenuIDs) > 0 {
		if err := h.deps.DB.Find(&menus, body.MenuIDs).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "菜单读取失败"})
			return
		}
	}
	if err := h.deps.DB.Model(&role).Association("Menus").Replace(menus); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "菜单绑定失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": role.ID})
}

func (h handler) bindAPIs(c *gin.Context) {
	role, ok := h.findRole(c)
	if !ok {
		return
	}
	var body struct {
		APIIDs []uint `json:"apiIds"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不对"})
		return
	}
	var apis []model.API
	if len(body.APIIDs) > 0 {
		if err := h.deps.DB.Find(&apis, body.APIIDs).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "接口读取失败"})
			return
		}
	}
	if err := h.deps.DB.Model(&role).Association("APIs").Replace(apis); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "接口绑定失败"})
		return
	}
	if h.deps.Gate != nil {
		if err := h.deps.Gate.Reload(h.deps.DB); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "权限刷新失败"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": role.ID})
}

func (h handler) listAPIs(c *gin.Context) {
	var apis []model.API
	if err := h.deps.DB.Find(&apis).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "接口列表读取失败"})
		return
	}
	c.JSON(http.StatusOK, apis)
}

func (h handler) createMenu(c *gin.Context) {
	var body struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要菜单名"})
		return
	}
	menu := model.Menu{Name: body.Name, Path: body.Path}
	if err := h.deps.DB.Create(&menu).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "菜单没有建成"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": menu.ID, "name": menu.Name})
}

func (h handler) loadUser(c *gin.Context) (model.User, bool) {
	var user model.User
	err := h.deps.DB.Preload("Roles.Menus").First(&user, c.GetUint("uid")).Error
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "登录状态已失效"})
		return user, false
	}
	return user, true
}

func (h handler) findRole(c *gin.Context) (model.Role, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	var role model.Role
	if err != nil || h.deps.DB.First(&role, id).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "角色不存在"})
		return role, false
	}
	return role, true
}

func roleNames(roles []model.Role) []string {
	names := make([]string, 0, len(roles))
	for _, role := range roles {
		names = append(names, role.Name)
	}
	return names
}

func menusOf(user model.User) []menuView {
	seen := map[uint]bool{}
	out := make([]menuView, 0)
	for _, role := range user.Roles {
		for _, menu := range role.Menus {
			if seen[menu.ID] {
				continue
			}
			seen[menu.ID] = true
			out = append(out, menuView{Name: menu.Name, Path: menu.Path})
		}
	}
	return out
}

func menusOfRole(role model.Role) []menuView {
	out := make([]menuView, 0, len(role.Menus))
	for _, menu := range role.Menus {
		out = append(out, menuView{Name: menu.Name, Path: menu.Path})
	}
	return out
}
