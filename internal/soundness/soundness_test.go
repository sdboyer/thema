package soundness

import (
	"cmp"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSoundness runs the oracles over every lineage that binds in the
// corpora. It is a spike (#324) and runs only with THEMA_SOUNDNESS=1; it
// writes what it found to THEMA_SOUNDNESS_OUT (default soundness.out in the
// test's directory), and fails nothing: the findings are triaged by hand.
func TestSoundness(t *testing.T) {
	if os.Getenv("THEMA_SOUNDNESS") == "" {
		t.Skip("set THEMA_SOUNDNESS=1 to run the soundness spike")
	}
	only := os.Getenv("THEMA_SOUNDNESS_CORPUS")
	outPath := cmp.Or(os.Getenv("THEMA_SOUNDNESS_OUT"), "soundness.out")
	lim := DefaultLimits
	if os.Getenv("THEMA_SOUNDNESS_WIDE") != "" {
		lim = WideLimits
	}

	type caseResult struct {
		e     entry
		st    Stats
		fails []Failure
	}
	var mu sync.Mutex
	results := map[string][]caseResult{}

	for _, co := range corpora {
		if only != "" && !slices.Contains(strings.Split(only, ","), co.name) {
			continue
		}
		t.Run(co.name, func(t *testing.T) {
			entries := co.load(t)
			sem := make(chan struct{}, 8)
			var wg sync.WaitGroup
			for _, e := range entries {
				if e.lin == nil {
					mu.Lock()
					results[co.name] = append(results[co.name], caseResult{e: e})
					mu.Unlock()
					continue
				}
				wg.Add(1)
				sem <- struct{}{}
				go func() {
					defer wg.Done()
					defer func() { <-sem }()
					st, fails := Run(co.name, e.name, e.lin, lim)
					mu.Lock()
					results[co.name] = append(results[co.name], caseResult{e: e, st: st, fails: fails})
					mu.Unlock()
				}()
			}
			wg.Wait()
		})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "limits: %+v\n\n", lim)
	var allFails []Failure
	for _, co := range corpora {
		rs := results[co.name]
		if len(rs) == 0 {
			continue
		}
		slices.SortFunc(rs, func(a, b caseResult) int { return strings.Compare(a.e.name, b.e.name) })
		var tot Stats
		bound, skipped := 0, 0
		skips := map[string]int{}
		var slow []string
		for _, r := range rs {
			if r.e.lin == nil {
				skipped++
				skips[r.e.skip]++
				continue
			}
			bound++
			tot.Schemas += r.st.Schemas
			tot.Candidates += r.st.Candidates
			tot.Discarded += r.st.Discarded
			tot.Instances += r.st.Instances
			tot.LeafPruned += r.st.LeafPruned
			tot.Capped += r.st.Capped
			tot.Edges += r.st.Edges
			tot.MinorSteps += r.st.MinorSteps
			tot.Translations += r.st.Translations
			tot.S3Leaves += r.st.S3Leaves
			tot.S3Computed += r.st.S3Computed
			tot.S4Pairs += r.st.S4Pairs
			tot.Twins += r.st.Twins
			tot.Elapsed += r.st.Elapsed
			tot.Unproducible = append(tot.Unproducible, prefixAll(r.e.name+" ", r.st.Unproducible)...)
			if r.st.Elapsed > 5*time.Second {
				slow = append(slow, fmt.Sprintf("%s (%s)", r.e.name, r.st.Elapsed.Round(time.Millisecond)))
			}
			allFails = append(allFails, r.fails...)
		}
		fmt.Fprintf(&b, "## %s\n", co.name)
		fmt.Fprintf(&b, "cases %d, bound %d, skipped %d\n", len(rs), bound, skipped)
		fmt.Fprintf(&b, "schemas %d, edges with a lens %d, minor steps %d\n", tot.Schemas, tot.Edges, tot.MinorSteps)
		fmt.Fprintf(&b, "candidates %d (filled twins %d), discarded %d, instances %d, leaf values pruned %d, nodes capped %d\n", tot.Candidates, tot.Twins, tot.Discarded, tot.Instances, tot.LeafPruned, tot.Capped)
		fmt.Fprintf(&b, "translations %d, S3 leaves %d (computed, unchecked: %d), S4 pairs %d, cpu time %s\n", tot.Translations, tot.S3Leaves, tot.S3Computed, tot.S4Pairs, tot.Elapsed.Round(time.Millisecond))
		if len(slow) > 0 {
			fmt.Fprintf(&b, "slow: %s\n", strings.Join(slow, ", "))
		}
		var keys []string
		for k := range skips {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  skip x%d: %s\n", skips[k], k)
		}
		for _, r := range rs {
			if r.e.lin == nil {
				fmt.Fprintf(&b, "  skipped %s: %s\n", r.e.name, r.e.skip)
			} else if r.e.adapted != "" {
				fmt.Fprintf(&b, "  adapted %s: %s\n", r.e.name, r.e.adapted)
			}
		}
		for _, u := range tot.Unproducible {
			fmt.Fprintf(&b, "  unproducible: %s\n", u)
		}
		b.WriteString("\n")
	}

	// Failures, grouped by cause, with the first witness and a count.
	type group struct {
		f Failure
		n int
	}
	groups := map[string]*group{}
	var order []string
	for _, f := range allFails {
		k := f.Key()
		if g, ok := groups[k]; ok {
			g.n++
			continue
		}
		groups[k] = &group{f: f, n: 1}
		order = append(order, k)
	}
	slices.SortFunc(order, func(a, b string) int { return strings.Compare(a, b) })
	byOracle := map[string]int{}
	for _, k := range order {
		byOracle[groups[k].f.Oracle]++
	}
	fmt.Fprintf(&b, "## failures: %d instances, %d groups %v\n\n", len(allFails), len(order), byOracle)
	for _, k := range order {
		g := groups[k]
		f := g.f
		fmt.Fprintf(&b, "%s %s/%s %s->%s x%d\n  %s\n  instance: %s\n", f.Oracle, f.Corpus, f.Case, f.From, f.To, g.n, f.Detail, f.Instance)
	}
	if err := os.WriteFile(outPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s: %d failure groups %v", outPath, len(order), byOracle)
}

func prefixAll(p string, ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = p + s
	}
	return out
}
