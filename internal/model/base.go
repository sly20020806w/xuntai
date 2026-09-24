package model

// 底座：菜单、用户、角色、接口。权限落在角色上。

type User struct {
	ID           uint   `gorm:"primaryKey"`
	Name         string `gorm:"size:64;not null"`
	PasswordHash string `gorm:"size:255"`
	Roles        []Role `gorm:"many2many:base_user_roles;"`
}

func (User) TableName() string { return "base_users" }

type Role struct {
	ID    uint   `gorm:"primaryKey"`
	Name  string `gorm:"size:64;not null"`
	Menus []Menu `gorm:"many2many:base_role_menus;"`
	APIs  []API  `gorm:"many2many:base_role_apis;"`
}

func (Role) TableName() string { return "base_roles" }

type Menu struct {
	ID       uint   `gorm:"primaryKey"`
	Name     string `gorm:"size:64;not null"`
	Path     string `gorm:"size:128"`
	ParentID *uint
}

func (Menu) TableName() string { return "base_menus" }

type API struct {
	ID     uint   `gorm:"primaryKey"`
	Method string `gorm:"size:16;not null"`
	Path   string `gorm:"size:255;not null"`
}

func (API) TableName() string { return "base_apis" }
