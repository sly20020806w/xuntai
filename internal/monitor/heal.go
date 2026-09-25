package monitor

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"xuntai/internal/model"
)

var (
	ErrNoPlaybook  = errors.New("没有这个剧本")
	ErrUnpublished = errors.New("剧本还没发布")
	ErrTemplate    = errors.New("入参模板要是对象")
	ErrTemplateVar = errors.New("入参模板里有不认识的变量")
	ErrNoRule      = errors.New("没有这条规则")
	ErrRuleNode    = errors.New("规则不在这个节点上")
	ErrRuleObject  = errors.New("规则不在这个对象上")
)

var alertVars = map[string]bool{
	"tree_node_id": true,
	"object_id":    true,
	"rule_name":    true,
	"fingerprint":  true,
	"summary":      true,
}

var placeholder = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// AlertVars 是告警带进剧本的上下文。模板改不了这几项。
type AlertVars struct {
	TreeNodeID  uint
	ObjectID    uint
	RuleName    string
	Fingerprint string
	Summary     string
}

// CheckBinding 只接受已发布的剧本，以及只含告警变量的对象模板。编号 0 表示不绑定。
func CheckBinding(db *gorm.DB, playbookID uint, template string) error {
	if err := ValidateTemplate(template); err != nil {
		return err
	}
	if playbookID == 0 {
		return nil
	}
	var book model.Playbook
	if err := db.First(&book, playbookID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNoPlaybook
		}
		return err
	}
	if book.Status != "published" {
		return ErrUnpublished
	}
	return nil
}

// RuleFits 要求规则盖住这次告警的节点。规则和告警都带了对象时，对象必须相同。
func RuleFits(db *gorm.DB, rule model.AlertRule, nodeID, objectID uint) error {
	ok, err := nodeCovers(db, rule.TreeNodeID, nodeID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrRuleNode
	}
	if rule.ObjectID != 0 && objectID != 0 && rule.ObjectID != objectID {
		return ErrRuleObject
	}
	return nil
}

// ValidateTemplate 空模板合法。非空时必须是 JSON 对象，占位符只能是告警上下文。
func ValidateTemplate(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil || obj == nil {
		return ErrTemplate
	}
	return walkPlaceholders(obj)
}

func walkPlaceholders(v any) error {
	switch t := v.(type) {
	case string:
		for _, match := range placeholder.FindAllStringSubmatch(t, -1) {
			if !alertVars[match[1]] {
				return ErrTemplateVar
			}
		}
	case map[string]any:
		for _, val := range t {
			if err := walkPlaceholders(val); err != nil {
				return err
			}
		}
	case []any:
		for _, val := range t {
			if err := walkPlaceholders(val); err != nil {
				return err
			}
		}
	}
	return nil
}

// Render 生成剧本入参。空模板只用告警上下文。合并后上下文字段以告警为准。
func Render(raw string, vars AlertVars) (map[string]any, error) {
	out := map[string]any{
		"tree_node_id": vars.TreeNodeID,
		"object_id":    vars.ObjectID,
		"rule_name":    vars.RuleName,
		"fingerprint":  vars.Fingerprint,
		"summary":      vars.Summary,
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out, nil
	}
	if err := ValidateTemplate(raw); err != nil {
		return nil, err
	}
	var extra map[string]any
	if err := json.Unmarshal([]byte(raw), &extra); err != nil || extra == nil {
		return nil, ErrTemplate
	}
	filled, ok := substitute(extra, vars).(map[string]any)
	if !ok {
		return nil, ErrTemplate
	}
	for key, val := range filled {
		out[key] = val
	}
	out["tree_node_id"] = vars.TreeNodeID
	out["object_id"] = vars.ObjectID
	out["rule_name"] = vars.RuleName
	out["fingerprint"] = vars.Fingerprint
	out["summary"] = vars.Summary
	return out, nil
}

func substitute(v any, vars AlertVars) any {
	switch t := v.(type) {
	case string:
		return substString(t, vars)
	case map[string]any:
		for key, val := range t {
			t[key] = substitute(val, vars)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = substitute(val, vars)
		}
		return t
	default:
		return v
	}
}

func substString(s string, vars AlertVars) any {
	name, exact := wholePlaceholder(s)
	if exact {
		switch name {
		case "tree_node_id":
			return vars.TreeNodeID
		case "object_id":
			return vars.ObjectID
		case "rule_name":
			return vars.RuleName
		case "fingerprint":
			return vars.Fingerprint
		case "summary":
			return vars.Summary
		}
	}
	return placeholder.ReplaceAllStringFunc(s, func(token string) string {
		match := placeholder.FindStringSubmatch(token)
		if len(match) < 2 {
			return token
		}
		switch match[1] {
		case "tree_node_id":
			return strconv.FormatUint(uint64(vars.TreeNodeID), 10)
		case "object_id":
			return strconv.FormatUint(uint64(vars.ObjectID), 10)
		case "rule_name":
			return vars.RuleName
		case "fingerprint":
			return vars.Fingerprint
		case "summary":
			return vars.Summary
		default:
			return token
		}
	})
}

func wholePlaceholder(s string) (string, bool) {
	s = strings.TrimSpace(s)
	match := placeholder.FindStringSubmatch(s)
	if match == nil || match[0] != s {
		return "", false
	}
	return match[1], true
}

// StepError 取这次执行里最后一条非空步骤错误。
func StepError(db *gorm.DB, runID uint) string {
	var steps []model.RunStep
	if err := db.Where("run_id = ?", runID).Order("seq").Find(&steps).Error; err != nil {
		return ""
	}
	last := ""
	for _, step := range steps {
		if strings.TrimSpace(step.Error) != "" {
			last = step.Error
		}
	}
	return last
}

// NoteRun 把终态写回自愈记录。还没有记录时不报错。
func NoteRun(db *gorm.DB, run *model.Run) error {
	if run == nil || run.ID == 0 {
		return nil
	}
	detail := ""
	if run.Status == "failed" || run.Status == "cancelled" {
		detail = StepError(db, run.ID)
	}
	return db.Model(&model.AlertAction{}).
		Where("run_id = ? AND action = ?", run.ID, "heal").
		Updates(map[string]any{"run_status": run.Status, "detail": detail}).Error
}
