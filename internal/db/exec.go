package db

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"

	"xuntai/internal/model"
	"xuntai/internal/task"
)

var (
	ErrWhitelist = errors.New("动作不在白名单里")
	ErrSQL       = errors.New("这条 SQL 不在白名单里")
	ErrApproval  = errors.New("变更要等工单审批通过")
	ErrProdExec  = errors.New("生产实例不走真实执行")
	ErrSameNode  = errors.New("工单不在这个节点上")
)

var (
	identRe      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	hostRe       = regexp.MustCompile(`^[A-Za-z0-9._%-]{1,64}$`)
	forbiddenSQL = regexp.MustCompile(`\b(drop|delete|truncate|insert|update|grant|revoke|union)\b`)
)

// Request 是一次已经审批过的数据库变更。平台不打开 SQL 连接。
type Request struct {
	InstanceID  uint
	TicketID    uint
	Action      string
	Database    string
	Account     string
	AccountHost string
	SQL         string
	Fail        bool
}

// Outcome 是下发后的任务。Issued 表示已交给代理，还没有结果。
type Outcome struct {
	JobID     uint
	Status    string
	Statement string
}

// Statement 把动作收成一条白名单语句。建库、建账号不接受调用方拼好的 SQL。
func Statement(req Request) (string, error) {
	switch strings.TrimSpace(req.Action) {
	case "create_database":
		name := strings.TrimSpace(req.Database)
		if !identRe.MatchString(name) || strings.EqualFold(name, "xuntai") {
			return "", ErrSQL
		}
		return "CREATE DATABASE " + name, nil
	case "create_account":
		name := strings.TrimSpace(req.Account)
		host := strings.TrimSpace(req.AccountHost)
		if host == "" {
			host = "%"
		}
		if !identRe.MatchString(name) || !hostRe.MatchString(host) || strings.ContainsAny(host, "'\"`;") {
			return "", ErrSQL
		}
		return fmt.Sprintf("CREATE USER '%s'@'%s'", name, host), nil
	case "sql_change":
		return controlledSQL(req.SQL)
	default:
		return "", ErrWhitelist
	}
}

// Apply 核对审批后，用任务中心下发白名单动作。模拟打开时自己回写结果，不连接数据库。
func Apply(db *gorm.DB, req Request, mock bool) (Outcome, error) {
	var out Outcome
	statement, err := Statement(req)
	if err != nil {
		return out, err
	}
	out.Statement = statement
	var instance model.Instance
	if err := db.First(&instance, req.InstanceID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, fmt.Errorf("没有这个实例")
		}
		return out, err
	}
	if instance.TreeNodeID == 0 {
		return out, fmt.Errorf("资源未挂载到服务树，无法校验归属")
	}
	if err := approved(db, req.TicketID, instance.TreeNodeID); err != nil {
		return out, err
	}
	env := strings.TrimSpace(instance.Env)
	if env != "开发" && env != "测试" {
		if !mock {
			return out, ErrProdExec
		}
	}
	script := model.Script{Name: "数据库变更", Content: statement}
	if err := db.Create(&script).Error; err != nil {
		return out, err
	}
	label := actionLabel(req.Action)
	job, err := task.Open(db, label, script.ID, instance.TreeNodeID, 1, []string{instance.Host})
	if err != nil {
		return out, err
	}
	out.JobID = job.ID
	if !mock {
		out.Status = "issued"
		return out, nil
	}
	status := "success"
	output := statement
	if req.Fail {
		status = "failed"
		output = "模拟执行未通过"
	}
	if err := task.Report(db, job.ID, instance.Host, status, output); err != nil {
		return out, err
	}
	out.Status = status
	if req.Fail {
		return out, fmt.Errorf("模拟执行未通过")
	}
	return out, nil
}

func approved(db *gorm.DB, ticketID, nodeID uint) error {
	if ticketID == 0 {
		return ErrApproval
	}
	var ticket model.TicketInstance
	if err := db.First(&ticket, ticketID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrApproval
		}
		return err
	}
	if ticket.TreeNodeID != nodeID {
		return ErrSameNode
	}
	if ticket.ApprovedBy == 0 || (ticket.Status != "pending_action" && ticket.Status != "finished") {
		return ErrApproval
	}
	return nil
}

func actionLabel(action string) string {
	switch action {
	case "create_database":
		return "建库"
	case "create_account":
		return "建账号"
	default:
		return "SQL变更"
	}
}

func controlledSQL(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	text = strings.TrimSuffix(text, ";")
	text = strings.TrimSpace(text)
	if text == "" || strings.Contains(text, ";") {
		return "", ErrSQL
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "--") || strings.Contains(lower, "/*") || strings.Contains(lower, "#") {
		return "", ErrSQL
	}
	for _, bad := range []string{"xuntai", "information_schema", "outfile", "dumpfile", "load_file", "sleep(", "benchmark("} {
		if strings.Contains(lower, bad) {
			return "", ErrSQL
		}
	}
	allowed := strings.HasPrefix(lower, "create table ") ||
		strings.HasPrefix(lower, "alter table ") ||
		strings.HasPrefix(lower, "create index ")
	if !allowed || forbiddenSQL.MatchString(lower) {
		return "", ErrSQL
	}
	return text, nil
}
