package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// serviceLogFile is the controller log of the Windows service in the logs folder.
const serviceLogFile = "crsp.log"

// serviceLogPath is the file the controller log goes to when it runs as a
// service, set before the controller starts; the controller logs it at startup
// so a missing log can be found. Empty when the log is on stderr.
var serviceLogPath string

// serviceLogProblems are the reasons the preferred log files could not be
// opened; the controller logs them at startup once a log is open.
var serviceLogProblems []string

// openLogFile opens the controller log of a service. A service has no console,
// so a log that fails to open would lose every line: it tries
// <dir>\crsp.log, then <dir>\crsp-<pid>.log (an ACL or a lock left by an
// upgrade can make the first unwritable), then <tmp>\crsp-service-<pid>.log.
// It returns the file, and the reasons the earlier candidates failed. The file
// is nil, with an error, only when none opened.
func openLogFile(dir, tmp string, pid int) (*os.File, []string, error) {
	var problems []string
	var errs []error
	candidates := []string{
		filepath.Join(dir, serviceLogFile),
		filepath.Join(dir, fmt.Sprintf("crsp-%d.log", pid)),
		filepath.Join(tmp, fmt.Sprintf("crsp-service-%d.log", pid)),
	}
	for i, path := range candidates {
		if i < 2 {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", dir, err))
				errs = append(errs, err)
				continue
			}
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640) //nolint:gosec // fixed log path
		if err == nil {
			return f, problems, nil
		}
		problems = append(problems, fmt.Sprintf("%s: %v", path, err))
		errs = append(errs, err)
	}
	return nil, problems, errors.Join(errs...)
}

// logServiceLogPath writes the first line of the controller log: which file it
// goes to and why a preferred file was not used, so an operator who finds no
// crsp.log knows where to look (operations, controller log).
func logServiceLogPath(logger *slog.Logger) {
	path := serviceLogPath
	if path == "" {
		path = "stderr"
	}
	logger.Info("controller log", "path", path, "version", version)
	for _, p := range serviceLogProblems {
		logger.Warn("controller log: a log file could not be opened", "problem", p)
	}
}
