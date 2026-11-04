package diag_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/sdboyer/thema/diag"
)

// From gives every error a join holds its diagnostics, as generators that
// run many inputs report their failures, though cue/errors finds only the
// first; a join under a wrap is found too. An error that joins nothing is
// one diagnostic, with its whole text as the detail.
func TestFromJoined(t *testing.T) {
	a := diag.New(diag.E0620).Headline("a").Err()
	b := diag.New(diag.E0616).Headline("b").Err()
	c := diag.New(diag.E0601).Headline("c").Err()
	for _, tc := range []struct {
		name string
		err  error
		want []string
	}{
		{"join", errors.Join(a, b), []string{"a", "b"}},
		{"wrapped join", fmt.Errorf("adapter failed: %w", errors.Join(a, fmt.Errorf("for input x: %w", b))), []string{"a", "b"}},
		{"nested join", errors.Join(a, errors.Join(b, c)), []string{"a", "b", "c"}},
		{"plain wrap", fmt.Errorf("context: %w", errors.New("cause")), []string{"context: cause"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, d := range diag.From(tc.err, diag.Context{}) {
				if d.Detail != "" {
					got = append(got, d.Detail)
					continue
				}
				got = append(got, d.Headline)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("headlines %q, want %q", got, tc.want)
			}
		})
	}
}
