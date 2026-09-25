package playbook

import "errors"

var (
	ErrConflict     = errors.New("已有未结束的执行")
	ErrNotPublished = errors.New("剧本还没发布")
	ErrNotFound     = errors.New("没有这个剧本")
	ErrForbidden    = errors.New("没有这个节点的运维权限")
	ErrBadState     = errors.New("现在不能这么做")
	ErrVersion      = errors.New("版本已经变了")
	ErrTerminal     = errors.New("执行已经结束")
)

type conflictError struct{ RunID uint }

func (e conflictError) Error() string { return ErrConflict.Error() }

func (e conflictError) Unwrap() error { return ErrConflict }
