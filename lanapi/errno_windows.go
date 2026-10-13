package lanapi

import "golang.org/x/sys/windows"

// Winsock errnos match none of the syscall.E* values, not even with errors.Is.
var (
	refusedErrs     = []error{windows.WSAECONNREFUSED}
	timeoutErrs     = []error{windows.WSAETIMEDOUT}
	unreachableErrs = []error{windows.WSAEHOSTUNREACH, windows.WSAENETUNREACH}
	resetErrs       = []error{windows.WSAECONNRESET, windows.WSAECONNABORTED}
)
