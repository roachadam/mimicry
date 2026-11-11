package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roachadam/mimicry/internal/rpc/client"
	"github.com/roachadam/mimicry/internal/rpc/pb"
	"github.com/roachadam/mimicry/profiles"
)

// The core built as released (CGO_ENABLED=0), started as an SDK starts it
// (client.Spawn: a socketpair, pipes on Windows), serves the protocol and
// exits when the SDK closes its end: 0 and a silent stderr after a clean
// close, even when a request through a proxy with credentials and a
// cookie failed; 1 and one line naming the protocol error after a broken
// frame, with no request data in it.
func TestCore(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "mimicry-core")

	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".") //nolint:gosec // bin is under t.TempDir()

	build.Env = append(os.Environ(), "CGO_ENABLED=0")

	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	for _, tc := range []struct {
		name   string
		talk   func(t *testing.T, c *client.Client, rwc interface{ Write([]byte) (int, error) })
		exit   int
		stderr string
	}{
		{
			name: "clean close",
			talk: func(t *testing.T, c *client.Client, _ interface{ Write([]byte) (int, error) }) {
				t.Helper()

				r, err := c.Call(t.Context(), &pb.Call{
					Method: &pb.Call_NewClient{
						NewClient: &pb.NewClient{
							Config: &pb.Config{ //nolint:gosec // a made-up proxy credential, which stderr must not show
								Profile: &pb.ProfileRef{
									Profile: &pb.ProfileRef_Name{
										Name: profiles.ChromeMacOS().Name(),
									},
								},
								Proxy: "http://user:hunter2@127.0.0.1:1",
							},
						},
					},
				})
				if err != nil {
					t.Fatal(err)
				}

				_, _, err = c.Do(t.Context(), r.GetNewClient().GetClient(), &pb.Request{
					Url: "https://shop.test/",
					Header: []*pb.HeaderValues{
						{
							Name: "Cookie",
							Values: [][]byte{
								[]byte("session=s3cret"),
							},
						},
					},
				}, nil)

				var e *client.Error
				if !errors.As(err, &e) || e.Proto.GetCode() != pb.Code_CODE_PROXY || strings.Contains(err.Error(), "hunter2") {
					t.Errorf("request: %v", err)
				}
			},
			exit: 0,
		},
		{
			name: "protocol error",
			talk: func(t *testing.T, _ *client.Client, rwc interface{ Write([]byte) (int, error) }) {
				t.Helper()

				// A header of zeros: type 0, which no frame has.
				if _, err := rwc.Write(make([]byte, 10)); err != nil {
					t.Fatal(err)
				}
			},
			exit:   1,
			stderr: "mimicry-core: wire: connection closed: wire: unknown frame type: 0 on stream 0\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer

			cmd := exec.CommandContext(t.Context(), bin)

			cmd.Stderr = &stderr

			rwc, err := client.Spawn(cmd)
			if err != nil {
				t.Fatal(err)
			}

			c, err := client.New(t.Context(), rwc, client.Config{})
			if err != nil {
				t.Fatal(err)
			}

			if c.HelloReply().GetCoreVersion() == "" {
				t.Error("no core version")
			}

			tc.talk(t, c, rwc)

			if tc.exit != 0 {
				select {
				case <-c.Done():
				case <-time.After(5 * time.Second):
					t.Error("the core kept the broken connection")
				}
			}

			c.Close()

			waited := make(chan error, 1)
			go func() {
				waited <- cmd.Wait()
			}()

			select {
			case err := <-waited:
				if err != nil && tc.exit == 0 {
					t.Errorf("the core: %v", err)
				}

			case <-time.After(10 * time.Second):
				t.Fatal("the core did not exit")
			}

			if got := cmd.ProcessState.ExitCode(); got != tc.exit {
				t.Errorf("exit code %d, want %d", got, tc.exit)
			}

			if stderr.String() != tc.stderr {
				t.Errorf("stderr %q, want %q", stderr.String(), tc.stderr)
			}
		})
	}
}
