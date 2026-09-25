package cmdb

import (
	"xuntai/internal/base"
	"xuntai/internal/model"

	"gorm.io/gorm"
)

func APICatalog() []model.API {
	return []model.API{
		{Method: "GET", Path: "/api/cmdb/models"},
		{Method: "POST", Path: "/api/cmdb/models"},
		{Method: "PUT", Path: "/api/cmdb/models/:id"},
		{Method: "DELETE", Path: "/api/cmdb/models/:id"},
		{Method: "GET", Path: "/api/cmdb/objects"},
		{Method: "POST", Path: "/api/cmdb/objects"},
		{Method: "PUT", Path: "/api/cmdb/objects/:id"},
		{Method: "DELETE", Path: "/api/cmdb/objects/:id"},
		{Method: "GET", Path: "/api/cmdb/object-nodes"},
		{Method: "POST", Path: "/api/cmdb/object-nodes"},
		{Method: "DELETE", Path: "/api/cmdb/object-nodes/:id"},
	}
}

func Seed(db *gorm.DB) (bool, error) {
	for _, role := range []string{"平台管理员", "节点运维"} {
		if err := base.Grant(db, role, APICatalog()); err != nil {
			return false, err
		}
	}
	return false, nil
}
