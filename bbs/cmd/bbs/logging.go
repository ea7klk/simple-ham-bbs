package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func appendLogFile(path, text string) {
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			if file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
				_, _ = file.WriteString(text)
				_ = file.Close()
				return
			}
		}
	}
	if err := os.WriteFile("/proc/1/fd/1", []byte(text), 0o600); err == nil {
		return
	}
	_, _ = fmt.Fprint(os.Stderr, text)
}

func (a *app) logBBSAction(actor, action, format string, args ...any) {
	actor = normalizeCallsign(actor)
	if actor == "" {
		actor = "-"
	}
	detail := ""
	if format != "" {
		if len(args) == 0 {
			detail = " " + singleLineAPRSDetail(format)
		} else {
			detail = " " + singleLineAPRSDetail(fmt.Sprintf(format, args...))
		}
	}
	appendLogFile(a.cfg.bbsLogFile, fmt.Sprintf("%s BBS action=%s user=%s%s\n", now(), action, actor, detail))
}
