package main

import (
	"context"
	"fmt"
	"os/exec"
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

// netem controls the client container's qdiscs.
type netem struct {
	dev string
	// ifb is set once ingress shaping works.
	ifb  bool
	note string
}

func newNetem(ctx context.Context, dev string) *netem {
	n := &netem{
		dev: dev,
	}

	// Segmentation offloads hand netem 64 KB super-packets, which it would
	// delay and drop whole; real links carry MTU-sized packets.
	if err := sh(ctx, "ethtool", "-K", dev, "tso", "off", "gso", "off", "gro", "off"); err != nil {
		n.note = "offloads left on (" + err.Error() + "); "
	}

	_ = sh(ctx, "ip", "link", "del", "ifb0")

	err := sh(ctx, "ip", "link", "add", "ifb0", "type", "ifb")
	if err == nil {
		err = sh(ctx, "ip", "link", "set", "ifb0", "up")
	}

	if err == nil {
		_ = sh(ctx, "tc", "qdisc", "del", "dev", dev, "ingress")
		err = sh(ctx, "tc", "qdisc", "add", "dev", dev, "handle", "ffff:", "ingress")
	}

	if err == nil {
		err = sh(ctx, "tc", "filter", "add", "dev", dev, "parent", "ffff:", "protocol", "all",
			"u32", "match", "u32", "0", "0", "action", "mirred", "egress", "redirect", "dev", "ifb0")
	}

	if err != nil {
		n.note += "no ifb device (" + err.Error() + "): the whole delay and the loss apply to outgoing packets only"

		return n
	}

	n.ifb = true

	n.note += "half the RTT and the loss on each direction (egress on " + dev + ", ingress through ifb0)"

	return n
}

// apply puts the interface under c (or clears it).
func (n *netem) apply(ctx context.Context, c condition) error {
	n.clear(ctx)

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
		return sh(ctx, "tc", args("ifb0")...)
	}

	return nil
}

func (n *netem) clear(ctx context.Context) {
	_ = sh(ctx, "tc", "qdisc", "del", "dev", n.dev, "root")
	if n.ifb {
		_ = sh(ctx, "tc", "qdisc", "del", "dev", "ifb0", "root")
	}
}

func sh(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // tc, ip and ethtool with arguments built here
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}

	return nil
}
