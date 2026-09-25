package cicd

import "errors"

var (
	ErrNeedTicket  = errors.New("need ticket")
	ErrTicketState = errors.New("ticket state")
	ErrTicketNode  = errors.New("ticket node")
	ErrNoStage     = errors.New("no stage")
	ErrNoInstance  = errors.New("no instance")
	ErrBadEnv      = errors.New("bad env")
	ErrNotLeaf     = errors.New("not leaf")
	ErrNotFound    = errors.New("not found")
	ErrProdStage   = errors.New("prod stage")
)
