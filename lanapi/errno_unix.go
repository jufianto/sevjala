//go:build !windows

package lanapi

import "syscall"

var (
	refusedErrs     = []error{syscall.ECONNREFUSED}
	timeoutErrs     = []error{syscall.ETIMEDOUT}
	unreachableErrs = []error{syscall.EHOSTUNREACH, syscall.ENETUNREACH}
	resetErrs       = []error{syscall.ECONNRESET, syscall.ECONNABORTED, syscall.EPIPE}
)
