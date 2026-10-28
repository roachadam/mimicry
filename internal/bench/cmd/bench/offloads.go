package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/roachadam/mimicry/internal/bench/target"
)

// Segmentation offloads under netem. Packets reach the client container
// through the Docker host's end of its veth pair. With the host's offloads
// on, they arrive as super-packets of up to 64 KB, made by the server's TSO
// or GSO or put together again by GRO on the host's end of the server's
// veth, and segmented nowhere on the way, so netem on the client delays and
// drops them whole: "1% loss" would drop several segments at a time. Under
// netem, layer 5 turns TSO, GSO and GRO off on the client's eth0 and,
// through the offloads sidecar (`bench offloads`, on the host's network),
// on the host's end of the client's veth, which then cuts every packet to
// the client into MSS-sized segments; both go back as they were for the
// conditions without netem.

// offloadFeatures are the offloads turned off: ethtool -K's name and
// ethtool -k's.
var offloadFeatures = [][2]string{
	{"tso", "tcp-segmentation-offload"},
	{"gso", "generic-segmentation-offload"},
	{"gro", "generic-receive-offload"},
}

// offloads are an interface's offloads as they were found, to put back.
type offloads struct {
	dev string
	// found are ethtool -K's arguments restoring them ("tso", "on", ...).
	found []string
}

// readOffloads reads dev's offloads.
func readOffloads(ctx context.Context, dev string) (*offloads, error) {
	out, err := exec.CommandContext(ctx, "ethtool", "-k", dev).Output() //nolint:gosec // an interface name, not a shell
	if err != nil {
		return nil, fmt.Errorf("ethtool -k %s: %w", dev, err)
	}

	o := &offloads{
		dev: dev,
	}

	for _, f := range offloadFeatures {
		state := "off"

		for line := range strings.Lines(string(out)) {
			name, value, ok := strings.Cut(strings.TrimSpace(line), ": ")
			if ok && name == f[1] {
				if strings.HasPrefix(value, "on") {
					state = "on"
				}

				break
			}
		}

		o.found = append(o.found, f[0], state)
	}

	return o, nil
}

// set turns the offloads off, or back as they were found.
func (o *offloads) set(ctx context.Context, off bool) error {
	args := []string{"-K", o.dev}
	if off {
		for _, f := range offloadFeatures {
			args = append(args, f[0], "off")
		}
	} else {
		args = append(args, o.found...)
	}

	return sh(ctx, "ethtool", args...)
}

// offloadsAgent is the sidecar's `bench offloads`, on the Docker host's
// network: PUT /offloads?ifindex=N&off=true turns the offloads of the
// host's veth with index N off, off=false puts them back as they were
// before the first change.
func offloadsAgent() error {
	var mu sync.Mutex

	found := map[string]*offloads{}

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /offloads", func(w http.ResponseWriter, r *http.Request) {
		off, err := strconv.ParseBool(r.URL.Query().Get("off"))
		if err != nil {
			http.Error(w, "off: "+err.Error(), http.StatusBadRequest)

			return
		}

		index, err := strconv.Atoi(r.URL.Query().Get("ifindex"))
		if err != nil {
			http.Error(w, "ifindex: "+err.Error(), http.StatusBadRequest)

			return
		}

		ifc, err := net.InterfaceByIndex(index)
		if err != nil || !strings.HasPrefix(ifc.Name, "veth") {
			http.Error(w, fmt.Sprintf("no veth with index %d", index), http.StatusNotFound)

			return
		}

		mu.Lock()
		defer mu.Unlock()

		o := found[ifc.Name]
		if o == nil {
			if o, err = readOffloads(r.Context(), ifc.Name); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)

				return
			}

			found[ifc.Name] = o
		}

		if err := o.set(r.Context(), off); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		w.WriteHeader(http.StatusNoContent)
	})

	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(target.OffloadsPort),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return srv.ListenAndServe()
}

// setPeerOffloads asks the sidecar, at the default gateway (the Docker
// host's end of the bench network), to turn off (or back) the offloads of
// the host's end of dev's veth pair.
func setPeerOffloads(ctx context.Context, dev string, off bool) error {
	peer, err := os.ReadFile("/sys/class/net/" + dev + "/iflink")
	if err != nil {
		return err
	}

	gw, err := defaultGateway()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	u := fmt.Sprintf("http://%s/offloads?ifindex=%s&off=%t",
		net.JoinHostPort(gw.String(), strconv.Itoa(target.OffloadsPort)), strings.TrimSpace(string(peer)), off)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("the offloads sidecar: %w", err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

		return fmt.Errorf("the offloads sidecar: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	return nil
}

// defaultGateway is the default route's gateway (/proc/net/route).
func defaultGateway() (net.IP, error) {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return nil, err
	}

	for line := range strings.Lines(string(b)) {
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}

		// Little-endian hex.
		v, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil {
			return nil, err
		}

		return net.IPv4(byte(v), byte(v>>8), byte(v>>16), byte(v>>24)), nil
	}

	return nil, errors.New("no default route")
}
