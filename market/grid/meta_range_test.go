package grid

import (
	"testing"

	"github.com/algoboy-kevin/go-common/market/feed"
)

// TestMetaDoesNotExtendRowRange (R2): the row range comes from DATA frames
// only. A _meta sentinel that arrives long after the last data frame must not
// emit extra seconds. Found by the parent's randomized differential trials
// (19 of 60 trials had a sentinel >= 1 s after the last data frame and the
// grid emitted up to 75 extra seconds versus the oracle).
func TestMetaDoesNotExtendRowRange(t *testing.T) {
	sink := &recSink{}
	g, err := New(DefaultConfig(), []string{"A"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.OnBBO(100e9, goodBBO("A", 1)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnBBO(102.5e9, goodBBO("A", 2)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnMeta(140e9, feed.KindBBO, MetaResubscribed); err != nil {
		t.Fatal(err)
	}
	if err := g.OnMeta(150e9, feed.KindL2Book, MetaDisconnected); err != nil {
		t.Fatal(err)
	}
	g.Flush(102.5e9) // replay end: Flush(maxDataRx)
	if got := g.Stats().SecondsEmitted; got != 3 {
		t.Fatalf("SecondsEmitted = %d, want 3 (T=100..102); a sentinel extended the row range", got)
	}
	if len(sink.ms) != 3 || len(sink.tf) != 3 {
		t.Fatalf("rows ms=%d tf=%d, want 3 and 3", len(sink.ms), len(sink.tf))
	}
}

// TestMetaDefersEmissionWithoutChangingRows: a sentinel between data frames
// does not emit by itself, but the rows emitted later still reflect it
// exactly: a gap opened at 110.4 s flags T >= 111 only (the window of T=110
// is (109, 110], which ends before the gap opens), and rule (a) closes it at
// the first later bbo frame (113.3 s), which still lies inside the window of
// T=114, so T=114 is flagged and T=115 is not.
func TestMetaDefersEmissionWithoutChangingRows(t *testing.T) {
	sink := &recSink{}
	g, err := New(DefaultConfig(), []string{"A"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.OnBBO(100e9, goodBBO("A", 1)); err != nil {
		t.Fatal(err)
	}
	if err := g.OnMeta(110.4e9, feed.KindBBO, MetaDisconnected); err != nil {
		t.Fatal(err)
	}
	if got := g.Stats().SecondsEmitted; got != 0 {
		t.Fatalf("OnMeta emitted %d seconds, want 0 (emission is driven by data frames)", got)
	}
	if err := g.OnMeta(112.2e9, feed.KindBBO, MetaResubscribed); err != nil {
		t.Fatal(err)
	}
	if err := g.OnBBO(113.3e9, goodBBO("A", 3)); err != nil { // first frame after resub: closes the gap (rule a)
		t.Fatal(err)
	}
	g.Flush(115e9)
	for T, want := range map[int64]bool{109: false, 110: false, 111: true, 112: true, 113: true, 114: true, 115: false} {
		eqNNBool(t, "feed_gap", sink.msAt(t, T, "A").FeedGap, want)
	}
}
