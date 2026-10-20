package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/shiva2021/rootout/internal/heur"
)

func TestFake(t *testing.T) {
	f := &Fake{Result: Assessment{Verdict: "Suspicious", Confidence: 0.5}}
	var p Provider = f
	a, err := p.Assess(context.Background(), heur.Report{Score: 10})
	if err != nil || a.Verdict != Suspicious || a.Provider != "fake" || len(f.Calls) != 1 {
		t.Errorf("a=%+v err=%v calls=%d", a, err, len(f.Calls))
	}
	boom := errors.New("boom")
	f.Err = boom
	if _, err := p.Assess(context.Background(), heur.Report{}); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestDecode(t *testing.T) {
	for _, in := range []string{"", "not json", `{"confidence":1}`} {
		if _, err := Decode(in); !errors.Is(err, ErrBadResponse) {
			t.Errorf("%q: err = %v", in, err)
		}
	}
}
