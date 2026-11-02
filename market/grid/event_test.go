package grid

// OnEvent (venue-neutral dispatch) tests: typed equivalence and non-data
// no-ops.

import (
	"reflect"
	"testing"

	"github.com/algoboy-kevin/go-common/market/feed"
)

// TestOnEventMatchesTypedDispatch (C.7f): a frame sequence fed through
// OnEvent produces exactly the rows of the same sequence fed through the
// typed On* methods.
func TestOnEventMatchesTypedDispatch(t *testing.T) {
	frames := []struct {
		rx int64
		ev feed.Event
	}{
		{100e9, feed.Event{Kind: feed.KindL2Book,
			L2: *mkL2("A", 100000, []feed.Level{{Px: 100, Sz: 2, N: 1}}, []feed.Level{{Px: 100.25, Sz: 4, N: 2}})}},
		{100.2e9, feed.Event{Kind: feed.KindBBO, BBO: *goodBBO("A", 100200)}},
		{100.4e9, feed.Event{Kind: feed.KindAssetCtx, Ctx: *mkCtx("A", 100.25, 100, 1000)}},
		{100.6e9, feed.Event{Kind: feed.KindTrades, Trades: []feed.Trade{
			mkTrade("A", 'B', 100, 1, 100600, 1),
			mkTrade("A", 'A', 100.5, 2, 100600, 2),
		}}},
		{101.3e9, feed.Event{Kind: feed.KindBBO, BBO: *goodBBO("A", 101300)}},
	}

	sinkEv := &recSink{}
	gEv, err := New(DefaultConfig(), []string{"A"}, sinkEv)
	if err != nil {
		t.Fatal(err)
	}
	for i := range frames {
		if err := gEv.OnEvent(frames[i].rx, &frames[i].ev); err != nil {
			t.Fatalf("OnEvent at %d: %v", frames[i].rx, err)
		}
	}
	gEv.Flush(102e9)

	sinkTyped := &recSink{}
	gTyped, err := New(DefaultConfig(), []string{"A"}, sinkTyped)
	if err != nil {
		t.Fatal(err)
	}
	for i := range frames {
		f := &frames[i]
		var err error
		switch f.ev.Kind {
		case feed.KindL2Book:
			err = gTyped.OnL2Book(f.rx, &f.ev.L2)
		case feed.KindBBO:
			err = gTyped.OnBBO(f.rx, &f.ev.BBO)
		case feed.KindTrades:
			err = gTyped.OnTrades(f.rx, f.ev.Trades)
		case feed.KindAssetCtx:
			err = gTyped.OnAssetCtx(f.rx, &f.ev.Ctx)
		}
		if err != nil {
			t.Fatalf("typed On* at %d: %v", f.rx, err)
		}
	}
	gTyped.Flush(102e9)

	if !reflect.DeepEqual(sinkEv.ms, sinkTyped.ms) {
		t.Error("OnEvent market_state rows differ from the typed On* path")
	}
	if !reflect.DeepEqual(sinkEv.tf, sinkTyped.tf) {
		t.Error("OnEvent trade_flow rows differ from the typed On* path")
	}
}

// TestOnEventNonDataNoEffect (C.7g): pong/meta/ack/unknown kinds return nil,
// change nothing (no rows, no stats, no watermark) and allocate nothing.
func TestOnEventNonDataNoEffect(t *testing.T) {
	sink := &recSink{}
	g, err := New(DefaultConfig(), []string{"A"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	for i, k := range []feed.Kind{feed.KindPong, feed.KindMeta, feed.KindAck, feed.KindUnknown} {
		ev := feed.Event{Kind: k}
		if err := g.OnEvent(int64(100e9)+int64(i), &ev); err != nil {
			t.Fatalf("kind %v: OnEvent = %v, want nil", k, err)
		}
	}
	if got := g.Stats(); got != (Stats{}) {
		t.Errorf("stats changed by non-data events: %+v", got)
	}
	if len(sink.ms) != 0 || len(sink.tf) != 0 {
		t.Errorf("non-data events emitted rows: %d ms, %d tf", len(sink.ms), len(sink.tf))
	}
	ev := feed.Event{Kind: feed.KindPong}
	if allocs := testing.AllocsPerRun(200, func() {
		if err := g.OnEvent(200e9, &ev); err != nil {
			panic(err)
		}
	}); allocs != 0 {
		t.Errorf("OnEvent with a non-data kind allocated %v times per run, want 0", allocs)
	}
}
