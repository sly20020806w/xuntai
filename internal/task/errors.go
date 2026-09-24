package task

import "errors"

var (
	ErrNoMachines  = errors.New("no machines")
	ErrDuplicate   = errors.New("duplicate host")
	ErrForeignHost = errors.New("foreign host")
	ErrNotIssued   = errors.New("not issued")
	ErrUnknownHost = errors.New("unknown host")
	ErrBadStatus   = errors.New("bad status")
	ErrNotRunning  = errors.New("not running")
	ErrNotPaused   = errors.New("not paused")
	ErrScript      = errors.New("script")
	ErrNode        = errors.New("node")
)
