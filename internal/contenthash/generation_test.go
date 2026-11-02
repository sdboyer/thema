package contenthash

import (
	"os"
	"testing"

	"golang.org/x/mod/modfile"
)

// CertifiedCUE moves with go.mod: a CUE upgrade there re-records the goldens,
// and either bumps Generation or certifies the current one against the new
// CUE.
func TestCertifiedCUEMatchesGoMod(t *testing.T) {
	b, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	f, err := modfile.Parse("go.mod", b, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range f.Replace {
		if r.Old.Path == "cuelang.org/go" {
			t.Fatalf("go.mod replaces cuelang.org/go with %s; digests computed with it are uncertified", r.New)
		}
	}
	for _, r := range f.Require {
		if r.Mod.Path == "cuelang.org/go" {
			if r.Mod.Version != CertifiedCUE {
				t.Errorf("go.mod requires cuelang.org/go %s, CertifiedCUE is %s", r.Mod.Version, CertifiedCUE)
			}
			return
		}
	}
	t.Fatal("go.mod does not require cuelang.org/go")
}

// thema's own build links the CUE its goldens were recorded against, so its
// digests are certified.
func TestLinkedCUECertified(t *testing.T) {
	if got := linkedCUE(); got != CertifiedCUE {
		t.Fatalf("linked cuelang.org/go %q, want %s", got, CertifiedCUE)
	}
	id := mustID(t, compileLineage(t, lineage(`{a: v1_0.a, b: v1_0.b}`)), Version{0, 0})
	for _, d := range []Digest{id.Schema, id.Lens} {
		if d.Generation != Generation || !d.Certified {
			t.Errorf("digest %s: generation %d, certified %t", d, d.Generation, d.Certified)
		}
	}
}

func TestDigestString(t *testing.T) {
	var sum [32]byte
	sum[0], sum[31] = 0xab, 0x01
	hex := "ab000000000000000000000000000000000000000000000000000000000000" + "01"
	for _, c := range []struct {
		d    Digest
		want string
	}{
		{Digest{Generation: 1, Certified: true, Sum: sum}, "g1." + hex},
		{Digest{Generation: 1, Certified: false, Sum: sum}, "g1u." + hex},
		{Digest{Generation: 12, Certified: true, Sum: sum}, "g12." + hex},
	} {
		if got := c.d.String(); got != c.want {
			t.Errorf("%+v: got %s, want %s", c.d, got, c.want)
		}
	}
}

// A digest of another generation, or an uncertified one, never equals the
// current one, even over the same canonical form.
func TestDigestEquality(t *testing.T) {
	d := digest("{}")
	other := d
	other.Generation++
	uncert := d
	uncert.Certified = !uncert.Certified
	if d == other || d == uncert {
		t.Errorf("%s equals %s or %s", d, other, uncert)
	}
	if d != digest("{}") {
		t.Error("digests of the same form differ")
	}
}
