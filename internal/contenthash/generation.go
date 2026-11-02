package contenthash

import (
	"runtime/debug"
	"strconv"
	"sync"
)

// Generation names the canonical form digests are taken over. It is bumped
// whenever that form's output changes for any reason: thema's rewrites, the
// cueview view it is built on, where a version's schema and lenses are found,
// or a CUE upgrade that changes how values are evaluated or exported. The
// golden digests in testdata fail on any of these; that failure is the prompt
// to bump. Generation 2 dropped lacuna messages from the lens form. Two
// digests of different generations are not comparable: they
// differ by generation, which says nothing about their content.
const Generation = 2

// CertifiedCUE is the version of cuelang.org/go that Generation's golden
// digests were recorded against, and that go.mod requires (a lockstep test
// holds the two together). Digests computed with any other CUE, as when a
// module in the build requires a newer cuelang.org/go and minimal version
// selection picks it, are uncertified.
const CertifiedCUE = "v0.18.0-rc.1"

// linkedCUE returns the version of cuelang.org/go linked into the running
// binary, following a replace directive, or "" when the build records none
// (no build info, or a replacement by a local directory). Test binaries
// record the build's modules only from Go 1.27, which is why go.mod requires
// it: under 1.26 every digest computed in a test was uncertified.
var linkedCUE = sync.OnceValue(func() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, m := range bi.Deps {
		if m.Path != "cuelang.org/go" {
			continue
		}
		if m.Replace != nil {
			m = m.Replace
		}
		return m.Version
	}
	return ""
})

// certified reports whether digests computed by the running binary are
// certified: whether it links the CUE that Generation was certified against.
func certified() bool {
	return linkedCUE() == CertifiedCUE
}

// tag returns the prefix that names a digest's generation and whether it is
// certified: g<generation>, with a u appended when it is not. Neither g nor u
// is a hex digit, and the tag never contains the separators . and :.
func tag(gen int, certified bool) string {
	t := "g" + strconv.Itoa(gen)
	if !certified {
		t += "u"
	}
	return t
}
