package capnp

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	capnp "capnproto.org/go/capnp/v3"
	"capnproto.org/go/capnp/v3/std/capnp/schema"
)

// layout runs the capnp tool on the schema text and returns the
// CodeGeneratorRequest it produces, which carries the authoritative layout of
// every struct: data and pointer section sizes, each field's offset, each
// union's discriminant. The file is written to a temporary directory, which
// is stripped from the request's display names, so the request depends only
// on the schema text and the tool's version.
func layout(tool, lineage, schemaText string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "thema-capnp-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, fieldName(lineage)+".capnp")
	const mode = 0o600
	if err = os.WriteFile(file, []byte(schemaText), mode); err != nil {
		return nil, err
	}
	cmd := exec.Command(tool, "compile", "-o-", "--src-prefix="+dir+string(filepath.Separator), file) //nolint:gosec // the tool is the caller's capnp executable
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("capnp compile: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// describeLayout renders a CodeGeneratorRequest as text, for goldens: each
// struct and enum node by its qualified name, in name order, with its sizes,
// fields with their offsets, discriminants and defaults. The request's bytes
// carry the capnp tool's version, so goldens record this rendering instead.
func describeLayout(b []byte) (string, error) {
	msg, err := capnp.Unmarshal(b)
	if err != nil {
		return "", err
	}
	req, err := schema.ReadRootCodeGeneratorRequest(msg)
	if err != nil {
		return "", err
	}
	nodes, err := req.Nodes()
	if err != nil {
		return "", err
	}
	type entry struct {
		name string
		text string
	}
	var entries []entry
	for i := 0; i < nodes.Len(); i++ {
		n := nodes.At(i)
		dn, _ := n.DisplayName()
		// The display name is <file>:<Outer>.<Nested>; the file is dropped.
		name := dn[strings.IndexByte(dn, ':')+1:]
		var sb strings.Builder
		switch n.Which() {
		case schema.Node_Which_structNode:
			sn := n.StructNode()
			fmt.Fprintf(&sb, "struct %s @0x%016x: data=%d ptrs=%d", name, n.Id(), sn.DataWordCount(), sn.PointerCount())
			if sn.DiscriminantCount() > 0 {
				fmt.Fprintf(&sb, " disc=%d@%d", sn.DiscriminantCount(), sn.DiscriminantOffset())
			}
			if sn.IsGroup() {
				sb.WriteString(" group")
			}
			sb.WriteString("\n")
			fs, _ := sn.Fields()
			for j := 0; j < fs.Len(); j++ {
				f := fs.At(j)
				fname, _ := f.Name()
				fmt.Fprintf(&sb, "  %s", fname)
				switch f.Which() {
				case schema.Field_Which_slot:
					ty, _ := f.Slot().Type()
					fmt.Fprintf(&sb, " @%d: %s off=%d", f.Ordinal().Explicit(), typeDesc(ty), f.Slot().Offset())
					if f.Slot().HadExplicitDefault() {
						dv, _ := f.Slot().DefaultValue()
						fmt.Fprintf(&sb, " default=%s", valueDesc(dv))
					}
				case schema.Field_Which_group:
					fmt.Fprintf(&sb, ": group @0x%016x", f.Group().TypeId())
				}
				if f.DiscriminantValue() != schema.Field_noDiscriminant {
					fmt.Fprintf(&sb, " disc=%d", f.DiscriminantValue())
				}
				sb.WriteString("\n")
			}
		case schema.Node_Which_enum:
			fmt.Fprintf(&sb, "enum %s @0x%016x\n", name, n.Id())
			es, _ := n.Enum().Enumerants()
			for j := 0; j < es.Len(); j++ {
				en, _ := es.At(j).Name()
				fmt.Fprintf(&sb, "  %s = %d\n", en, j)
			}
		default:
			continue
		}
		entries = append(entries, entry{name, sb.String()})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	var out strings.Builder
	for _, e := range entries {
		out.WriteString(e.text)
	}
	return out.String(), nil
}

func typeDesc(t schema.Type) string {
	switch t.Which() {
	case schema.Type_Which_list:
		et, _ := t.List().ElementType()
		return "List(" + typeDesc(et) + ")"
	case schema.Type_Which_enum:
		return fmt.Sprintf("enum@0x%016x", t.Enum().TypeId())
	case schema.Type_Which_structType:
		return fmt.Sprintf("struct@0x%016x", t.StructType().TypeId())
	}
	return t.Which().String()
}

func valueDesc(v schema.Value) string {
	switch v.Which() {
	case schema.Value_Which_bool:
		return fmt.Sprint(v.Bool())
	case schema.Value_Which_int8:
		return fmt.Sprint(v.Int8())
	case schema.Value_Which_int16:
		return fmt.Sprint(v.Int16())
	case schema.Value_Which_int32:
		return fmt.Sprint(v.Int32())
	case schema.Value_Which_int64:
		return fmt.Sprint(v.Int64())
	case schema.Value_Which_uint8:
		return fmt.Sprint(v.Uint8())
	case schema.Value_Which_uint16:
		return fmt.Sprint(v.Uint16())
	case schema.Value_Which_uint32:
		return fmt.Sprint(v.Uint32())
	case schema.Value_Which_uint64:
		return fmt.Sprint(v.Uint64())
	case schema.Value_Which_text:
		s, _ := v.Text()
		return quote(s)
	case schema.Value_Which_data:
		d, _ := v.Data()
		return fmt.Sprintf("0x%x", d)
	case schema.Value_Which_enum:
		return fmt.Sprintf("enum(%d)", v.Enum())
	}
	return v.Which().String()
}
