// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"fmt"
	"os"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

type CheckFatalHandler func(err error, msg string)

// This function should only be called in the main.go of a service.
func NewCheckFatalHandler(logger log.Logger, flush func() error) CheckFatalHandler {
	l := level.Error(logger)
	// Since the call of logger.Log(...) happens on a different level on the
	// callstack, we would need to override the "caller" keyval to get the
	// original filename. This is unfortunately not possible.
	// Therefore we add the "original_caller" keyval.
	l = log.With(l, "original_caller", log.Caller(4)) // one level deeper than log.DefaultCaller
	return func(err error, msg string) {
		if err != nil {
			if msg != "" {
				l = log.With(l, "msg", msg)
			}
			l.Log("err", fmt.Sprintf("%+v", err))
			if flush != nil {
				if err = flush(); err != nil {
					fmt.Fprintln(os.Stderr, "Could not flush logger", err)
				}
			}
			os.Exit(1)
		}
	}
}
