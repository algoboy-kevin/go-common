// Package feedtest holds the venue-neutral FrameDecoder contract runner.
// Every venue adapter's decoder test calls RunDecoderContract with its own
// frames, so "implements the feed contract" means the same thing for every
// exchange.
package feedtest

import (
	"errors"
	"reflect"
	"testing"

	"github.com/algoboy-kevin/go-common/market/feed"
)

// Case is one known-good data frame with its expected classification and
// coin. Coin is checked against the member Kind selects; for trades the
// first print's Coin.
type Case struct {
	Name  string
	Frame []byte
	Kind  feed.Kind
	Coin  string
}

// Contract describes one venue adapter's decoder for RunDecoderContract.
type Contract struct {
	New         func() feed.FrameDecoder
	Cases       []Case
	NonData     [][]byte // must return nil with Kind in {KindPong, KindMeta, KindAck}
	Garbage     [][]byte // must error (ErrMalformed or ErrNotThisChannel), never panic
	UnknownCoin []byte   // must error with errors.Is(err, feed.ErrUnknownCoin)
}

// RunDecoderContract runs the feed FrameDecoder contract against c. Each
// contract clause is its own subtest: (a) decode and classification,
// (b) event reuse leaks nothing, (c) non-data frames, (d) garbage never
// panics and always errors, (e) unknown coin, (f) zero allocations.
func RunDecoderContract(t *testing.T, c Contract) {
	t.Helper()
	if c.New == nil {
		t.Fatal("Contract.New is nil")
	}
	dec := c.New()

	t.Run("decode", func(t *testing.T) {
		for _, cs := range c.Cases {
			cs := cs
			t.Run(cs.Name, func(t *testing.T) {
				var ev feed.Event
				if err := dec.Decode(cs.Frame, &ev); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if ev.Kind != cs.Kind {
					t.Fatalf("kind = %v, want %v", ev.Kind, cs.Kind)
				}
				if got := eventCoin(&ev, cs.Kind); got != cs.Coin {
					t.Fatalf("coin = %q, want %q", got, cs.Coin)
				}
			})
		}
	})

	t.Run("event_reuse", func(t *testing.T) {
		var shared feed.Event
		for _, ci := range c.Cases {
			for _, cj := range c.Cases {
				if err := dec.Decode(ci.Frame, &shared); err != nil {
					t.Fatalf("decode i=%s: %v", ci.Name, err)
				}
				if err := dec.Decode(cj.Frame, &shared); err != nil {
					t.Fatalf("decode j=%s after i=%s: %v", cj.Name, ci.Name, err)
				}
				var fresh feed.Event
				if err := dec.Decode(cj.Frame, &fresh); err != nil {
					t.Fatalf("fresh decode j=%s: %v", cj.Name, err)
				}
				got, want := eventMember(&shared, cj.Kind), eventMember(&fresh, cj.Kind)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("member for %s after %s leaks: got %+v, want %+v", cj.Name, ci.Name, got, want)
				}
			}
		}
	})

	t.Run("non_data", func(t *testing.T) {
		for i, frame := range c.NonData {
			var ev feed.Event
			if err := dec.Decode(frame, &ev); err != nil {
				t.Fatalf("frame %d: non-data frame must return nil, got %v", i, err)
			}
			switch ev.Kind {
			case feed.KindPong, feed.KindMeta, feed.KindAck:
			default:
				t.Fatalf("frame %d: kind = %v, want KindPong/KindMeta/KindAck", i, ev.Kind)
			}
		}
	})

	t.Run("garbage", func(t *testing.T) {
		for i, frame := range c.Garbage {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("frame %d panicked: %v", i, r)
					}
				}()
				var ev feed.Event
				err := dec.Decode(frame, &ev)
				if err == nil {
					t.Fatalf("frame %d (%q): garbage must error", i, frame)
				}
				if !errors.Is(err, feed.ErrMalformed) && !errors.Is(err, feed.ErrNotThisChannel) {
					t.Fatalf("frame %d: error %v wraps neither ErrMalformed nor ErrNotThisChannel", i, err)
				}
			}()
		}
	})

	t.Run("unknown_coin", func(t *testing.T) {
		var ev feed.Event
		err := dec.Decode(c.UnknownCoin, &ev)
		if !errors.Is(err, feed.ErrUnknownCoin) {
			t.Fatalf("got %v, want ErrUnknownCoin", err)
		}
	})

	t.Run("zero_allocs", func(t *testing.T) {
		var ev feed.Event
		for _, cs := range c.Cases {
			if err := dec.Decode(cs.Frame, &ev); err != nil { // warm-up: grow ev.Trades
				t.Fatalf("warm-up %s: %v", cs.Name, err)
			}
		}
		for _, cs := range c.Cases {
			allocs := testing.AllocsPerRun(200, func() {
				if err := dec.Decode(cs.Frame, &ev); err != nil {
					panic(err) // unreachable on the success path
				}
			})
			if allocs != 0 {
				t.Errorf("%s: Decode allocated %v times per run, want 0", cs.Name, allocs)
			}
		}
	})
}

// eventCoin returns the coin of the member Kind selects (trades: first
// print), or "" when that member is empty or kind is not a data kind.
func eventCoin(ev *feed.Event, k feed.Kind) string {
	switch k {
	case feed.KindL2Book:
		return ev.L2.Coin
	case feed.KindBBO:
		return ev.BBO.Coin
	case feed.KindAssetCtx:
		return ev.Ctx.Coin
	case feed.KindTrades:
		if len(ev.Trades) == 0 {
			return ""
		}
		return ev.Trades[0].Coin
	}
	return ""
}

// eventMember returns the member of ev that Kind selects, as a value
// suitable for reflect.DeepEqual.
func eventMember(ev *feed.Event, k feed.Kind) any {
	switch k {
	case feed.KindL2Book:
		return ev.L2
	case feed.KindBBO:
		return ev.BBO
	case feed.KindAssetCtx:
		return ev.Ctx
	case feed.KindTrades:
		return ev.Trades
	}
	return nil
}
