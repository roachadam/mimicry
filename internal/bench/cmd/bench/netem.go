package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// condition is a network condition the client container's interface is
// put under with tc netem: half the round trip on the way out (eth0) and
// half on the way in (an ifb device mirroring eth0's ingress), the loss on
// both. Without ifb the whole delay and the loss apply on the way out.
type condition struct {
	Name  string        `json:"name"`
	RTT   time.Duration `json:"rtt"`
	Loss  float64       `json:"loss"`
	Netem bool          `json:"netem"`
}

// conditions are layer 5's network conditions; the first is the network as
// it is.
var conditions = []condition{
	{
		Name: "none",
	},
	{
		Name:  "rtt20",
		RTT:   20 * time.Millisecond,
		Netem: true,
	},
	{
		Name:  "rtt20-loss1",
		RTT:   20 * time.Millisecond,
		Loss:  1,
		Netem: true,
	},
	{
		Name:  "rtt80",
		RTT:   80 * time.Millisecond,
		Netem: true,
	},
	{
		Name:  "rtt80-loss1",
		RTT:   80 * time.Millisecond,
		Loss:  1,
		Netem: true,
	},
}

func (c condition) String() string {
	if !c.Netem {
		return "no netem"
	}

	return fmt.Sprintf("RTT %v, loss %g%%", c.RTT, c.Loss)
}

// netem controls the client container's qdiscs, and the segmentation
// offloads of both ends of its veth pair (offloads.go).
type netem struct {
	dev string
	// ifb is set once ingress shaping works.
	ifb  bool
	note string
	// client are the client's offloads, nil if they could not be read;
	// off is whether they and the host's end's are off now; offErrs are
	// the failures to change them, each once.
	client  *offloads
	off     bool
	offErrs []string
}

func newNetem(ctx context.Context, dev string) *netem {
	n := &netem{
		dev: dev,
	}

	o, err := readOffloads(ctx, dev)
	if err != nil {
		n.offError(err)
	}

	n.client = o

	_ = sh(ctx, "ip", "link", "del", "ifb0")

	err = sh(ctx, "ip", "link", "add", "ifb0", "type", "ifb")
	if err == nil {
		err = sh(ctx, "ip", "link", "set", "ifb0", "up")
	}

	if err == nil {
		// A trial of the redirect the netem conditions use; it comes off
		// again until one of them applies.
		err = n.redirect(ctx, true)
		_ = n.redirect(ctx, false)
	}

	if err != nil {
		_ = sh(ctx, "ip", "link", "del", "ifb0")
		n.note = "no ifb device (" + err.Error() + "): the whole delay and the loss apply to outgoing packets only"

		return n
	}

	n.ifb = true

	n.note = "half the RTT and the loss on each direction (egress on " + dev + ", ingress through ifb0)"

	return n
}

// redirect sends dev's incoming packets through ifb0, whose netem shapes
// them, or takes the redirect away. Only a netem condition has it: ifb0
// without its netem queues at most 32 packets (pfifo_fast at its
// txqueuelen), which dropped about 5% of the packets to the client under
// layer 6's load when the redirect outlived layer 5 (bench-2026-10-29
// before its rerun, 396bdbf).
func (n *netem) redirect(ctx context.Context, on bool) error {
	_ = sh(ctx, "tc", "qdisc", "del", "dev", n.dev, "ingress")

	if !on {
		return nil
	}

	if err := sh(ctx, "tc", "qdisc", "add", "dev", n.dev, "handle", "ffff:", "ingress"); err != nil {
		return err
	}

	return sh(ctx, "tc", "filter", "add", "dev", n.dev, "parent", "ffff:", "protocol", "all",
		"u32", "match", "u32", "0", "0", "action", "mirred", "egress", "redirect", "dev", "ifb0")
}

// summary is the report's note on the shaping: n.note, and the offloads.
func (n *netem) summary() string {
	s := n.note + "; segmentation offloads (TSO, GSO, GRO) off under netem on both ends of the client's veth, so " +
		"packets reach netem one MSS at most and a loss drops one segment"
	if len(n.offErrs) > 0 {
		s += ", except: " + strings.Join(n.offErrs, "; ")
	}

	return s
}

// offError records a failure to change the offloads, once per message.
func (n *netem) offError(err error) {
	fmt.Fprintln(os.Stderr, "bench: offloads:", err)

	if msg := err.Error(); !slices.Contains(n.offErrs, msg) {
		n.offErrs = append(n.offErrs, msg)
	}
}

// offloads turns the offloads of both ends of the client's veth off, or
// back.
func (n *netem) offloads(ctx context.Context, off bool) {
	if off == n.off {
		return
	}

	n.off = off

	if n.client != nil {
		if err := n.client.set(ctx, off); err != nil {
			n.offError(err)
		}
	}

	if err := setPeerOffloads(ctx, n.dev, off); err != nil {
		n.offError(err)
	}
}

// apply puts the interface under c (or clears it), with the offloads off
// under netem.
func (n *netem) apply(ctx context.Context, c condition) error {
	n.clear(ctx)
	n.offloads(ctx, c.Netem)

	if !c.Netem {
		return nil
	}

	delay := c.RTT
	if n.ifb {
		delay /= 2
	}

	args := func(dev string) []string {
		a := []string{"qdisc", "replace", "dev", dev, "root", "netem", "limit", "100000",
			"delay", strconv.FormatInt(delay.Microseconds(), 10) + "us"}
		if c.Loss > 0 {
			a = append(a, "loss", strconv.FormatFloat(c.Loss, 'f', -1, 64)+"%")
		}

		return a
	}

	if err := sh(ctx, "tc", args(n.dev)...); err != nil {
		return err
	}

	if n.ifb {
		// ifb0's netem before the redirect into it.
		if err := sh(ctx, "tc", args("ifb0")...); err != nil {
			return err
		}

		return n.redirect(ctx, true)
	}

	return nil
}

// clear takes the shaping off, the ingress redirect included.
func (n *netem) clear(ctx context.Context) {
	_ = sh(ctx, "tc", "qdisc", "del", "dev", n.dev, "root")
	if n.ifb {
		_ = n.redirect(ctx, false)
		_ = sh(ctx, "tc", "qdisc", "del", "dev", "ifb0", "root")
	}
}

// restore clears the shaping, removes ifb0 and puts the offloads back: the
// layers after see the interface as newNetem found it.
func (n *netem) restore(ctx context.Context) {
	n.clear(ctx)

	if n.ifb {
		_ = sh(ctx, "ip", "link", "del", "ifb0")
	}

	n.offloads(ctx, false)
}

func sh(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // tc, ip and ethtool with arguments built here
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}

	return nil
}
