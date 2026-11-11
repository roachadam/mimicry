package server

import "syscall"

// pipeGone are the errors a write to (or read from) one of Windows'
// anonymous pipes returns once the SDK closed its end: ERROR_BROKEN_PIPE,
// ERROR_NO_DATA ("the pipe is being closed") and ERROR_PIPE_NOT_CONNECTED.
var pipeGone = []error{
	syscall.ERROR_BROKEN_PIPE,
	syscall.Errno(232),
	syscall.Errno(233),
}
