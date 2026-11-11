// Package pb is the Go code protoc-gen-go generates from the schema in
// proto/mimicry/v1 (decision 0069): the messages of mimicry-core's
// protocol. Never edit mimicry.pb.go: edit the schema, then run
// `moon run mimicry:proto` (buf, at the version .moon/tasks pins, with
// protoc-gen-go at go.mod's version). TestGenerated fails while the
// checked-in code differs from what the schema generates.
package pb

// The protocol version this schema is: Hello carries it. A change an
// older peer cannot read moves ProtocolMajor; an addition it can ignore
// moves ProtocolMinor.
const (
	ProtocolMajor = 1
	ProtocolMinor = 0
)
