package pool

import "context"

type partitionKey struct{}

// WithPartition returns ctx asking for connections of partition p only:
// requests in different partitions never share a connection, as a browser
// keeps credential-less requests or a cross-site frame's apart. Empty is
// the default partition.
func WithPartition(ctx context.Context, p string) context.Context {
	if p == "" {
		return ctx
	}
	return context.WithValue(ctx, partitionKey{}, p)
}

// Partition returns the partition ctx asks for, or "".
func Partition(ctx context.Context) string {
	p, _ := ctx.Value(partitionKey{}).(string)
	return p
}
