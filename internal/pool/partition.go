package pool

import "context"

type partitionKey struct{}

// WithPartition returns ctx asking for connections of partition p only;
// partitions never share connections (decision 0011). Empty is the
// default partition.
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
