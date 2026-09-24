package platform

// Modules 是八个模块的依赖顺序。数据库只登记实例和备份，不进集群。
var Modules = []string{
	"base",
	"tree",
	"ticket",
	"task",
	"monitor",
	"k8s",
	"cicd",
	"db",
}
