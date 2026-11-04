package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/heur"
)

func TestFake(t *testing.T) {
	f := &Fake{Result: Assessment{Verdict: "Suspicious", Confidence: 0.5},
		AppResult: AppAssessment{Purpose: "Edits text.", Source: "LEGITIMATE", Confidence: 90, Recommendation: "keep"}}
	var p Provider = f
	a, err := Assess(context.Background(), p, heur.Report{Score: 10})
	if err != nil || a.Verdict != Suspicious || a.Provider != "fake" || len(f.Calls) != 1 {
		t.Errorf("a=%+v err=%v calls=%d", a, err, len(f.Calls))
	}
	b, err := AssessApp(context.Background(), p, app.Facts{App: app.App{Name: "Foo"}})
	if err != nil || b.Source != SourceLegitimate || b.Confidence != 0.9 || b.Recommendation != Keep || b.Provider != "fake" || len(f.Calls) != 2 {
		t.Errorf("b=%+v err=%v calls=%d", b, err, len(f.Calls))
	}
	boom := errors.New("boom")
	f.Err = boom
	if _, err := Assess(context.Background(), p, heur.Report{}); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	if _, err := AssessApp(context.Background(), p, app.Facts{}); !errors.Is(err, boom) {
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
