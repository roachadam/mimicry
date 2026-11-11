package pb

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/cmd/protoc-gen-go/internal_gengo"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/pluginpb"
)

// The schema's files, relative to the buf module (proto/).
var schema = []string{
	"mimicry/v1/mimicry.proto",
}

// The checked-in code is what protoc-gen-go, at go.mod's version, makes of
// the schema: compiled here as buf compiles it (protocompile), then
// generated with buf.gen.yaml's options. A schema edited without
// regenerating fails it.
func TestGenerated(t *testing.T) {
	files, err := (&protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			ImportPaths: []string{
				"../../../proto",
			},
		}),
		SourceInfoMode: protocompile.SourceInfoStandard,
	}).Compile(t.Context(), schema...)
	if err != nil {
		t.Fatal(err)
	}

	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: schema,
		Parameter:      new("module=github.com/roachadam/mimicry/internal/rpc/pb"),
	}

	seen := map[string]bool{}

	var add func(protoreflect.FileDescriptor)

	add = func(fd protoreflect.FileDescriptor) {
		if seen[fd.Path()] {
			return
		}

		seen[fd.Path()] = true

		imports := fd.Imports()
		for i := range imports.Len() {
			add(imports.Get(i).FileDescriptor)
		}

		fdp := protodesc.ToFileDescriptorProto(fd)
		if !slices.Contains(schema, fd.Path()) {
			// buf sends imports without their source info.
			fdp.SourceCodeInfo = nil
		}

		req.ProtoFile = append(req.ProtoFile, fdp)
	}

	for _, f := range files {
		add(f)
	}

	gen, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range gen.Files {
		if f.Generate {
			internal_gengo.GenerateFile(gen, f)
		}
	}

	resp := gen.Response()
	if resp.Error != nil {
		t.Fatal(resp.GetError())
	}

	if len(resp.File) != len(schema) {
		t.Fatalf("generated %d files, want %d", len(resp.File), len(schema))
	}

	for _, f := range resp.File {
		got, err := os.ReadFile(filepath.FromSlash(f.GetName()))
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(got, []byte(f.GetContent())) {
			t.Errorf("internal/rpc/pb/%s is stale; run moon run mimicry:proto", f.GetName())
		}
	}
}
