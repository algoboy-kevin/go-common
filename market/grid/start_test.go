package grid

import (
	"github.com/algoboy-kevin/go-common/market/feed"
	"testing"
)

func TestStartGlobalRange(t *testing.T) {
	s := &recSink{}
	g, err := New(DefaultConfig(), []string{"A"}, s)
	if err != nil {
		t.Fatal(err)
	}
	g.EnableFunding(nil, NewMapSettled(nil))
	if err := g.OnMeta(99e9, feed.KindBBO, MetaDisconnected); err != nil {
		t.Fatal(err)
	}
	if err := g.Start(100e9 + 1); err != nil {
		t.Fatal(err)
	}
	if !g.started || g.nextT != 101 || g.minRxNs != 100e9+1 || !g.insts[0].fs.open || g.insts[0].fs.curS != 3600 {
		t.Fatalf("incorrect global start: %+v", g)
	}
	if err := g.Start(102e9); err != nil {
		t.Fatal(err)
	}
	if g.nextT != 101 || g.minRxNs != 100e9+1 || len(s.ms) != 0 {
		t.Fatal("repeated Start changed range or emitted")
	}
	if err := g.Start(101e9); err != ErrOutOfOrder {
		t.Fatalf("old Start: %v", err)
	}
	g.Flush(102e9)
	if len(s.ms) != 2 || s.ms[0].TsS != 101 || s.ms[0].BboValid {
		t.Fatalf("quiet rows: %+v", s.ms)
	}
	if err := g.Start(102e9); err != ErrOutOfOrder {
		t.Fatalf("watermark Start: %v", err)
	}
}
