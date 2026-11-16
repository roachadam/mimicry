package server

import "syscall"

// Windows' errors for a pipe whose other end is closing or gone, which
// syscall does not name.
const (
	errorNoData           = syscall.Errno(232) // ERROR_NO_DATA: the pipe is being closed
	errorPipeNotConnected = syscall.Errno(233) // ERROR_PIPE_NOT_CONNECTED
)

// pipeGone are the errors a write to (or read from) one of Windows'
// anonymous pipes returns once the SDK closed its end.
var pipeGone = []error{
	syscall.ERROR_BROKEN_PIPE,
	errorNoData,
	errorPipeNotConnected,
}
