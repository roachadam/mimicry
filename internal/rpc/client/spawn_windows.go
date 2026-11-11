package client

import (
	"io"
	"os/exec"
)

// Spawn starts cmd (mimicry-core) as an SDK does on Windows: anonymous
// pipes as its stdin and stdout (decision 0069), returned as one
// connection. Closing it makes the core exit.
func Spawn(cmd *exec.Cmd) (io.ReadWriteCloser, error) {
	w, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	r, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return pipes{
		r: r,
		w: w,
	}, nil
}

// pipes is the core's stdout to read and its stdin to write.
type pipes struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (p pipes) Read(b []byte) (int, error) {
	return p.r.Read(b)
}

func (p pipes) Write(b []byte) (int, error) {
	return p.w.Write(b)
}

func (p pipes) Close() error {
	err := p.w.Close()

	_ = p.r.Close()

	return err
}
