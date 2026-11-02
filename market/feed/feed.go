// Package feed is the venue-neutral decoded-event contract of the engine:
// every exchange adapter (Hyperliquid, Binance-style perps, spot, ...)
// decodes its raw frames into these types and nothing downstream knows the
// venue. Adding an exchange means writing one adapter package that
// implements FrameDecoder; the grid consumes only feed.Event.
//
// Nullability discipline: a field whose presence flag (BBO.HasBid/HasAsk,
// AssetCtx.Have, AssetCtx.HasImpact) is clear is zeroed and must read as
// NULL downstream, never as an observed 0.
package feed

import "errors"

// Kind classifies a raw frame by its top-level "channel" value. Frames are
// classified by channel name only, never by the presence of a "data" member
// (the subscriptionResponse ack carries "data" but is not market data).
type Kind uint8

const (
	KindUnknown Kind = iota
	KindL2Book
	KindBBO
	KindTrades
	KindAssetCtx
	KindPong
	KindMeta
	KindAck
)

// String returns the venue channel name the kind was classified from, or
// "unknown".
func (k Kind) String() string {
	switch k {
	case KindL2Book:
		return "l2Book"
	case KindBBO:
		return "bbo"
	case KindTrades:
		return "trades"
	case KindAssetCtx:
		return "activeAssetCtx"
	case KindPong:
		return "pong"
	case KindMeta:
		return "_meta"
	case KindAck:
		return "subscriptionResponse"
	default:
		return "unknown"
	}
}

// Trade side codes. SideBuy means the taker bought (lifted the ask);
// SideSell means the taker sold (hit the bid).
const (
	SideBuy  uint8 = 'B'
	SideSell uint8 = 'A'
)

// CtxFields are presence bits for the seven nullable ctx numbers of
// AssetCtx. A field whose bit is clear in AssetCtx.Have is zeroed and must
// read as NULL downstream, never as 0.
type CtxFields uint8

const (
	CtxFunding CtxFields = 1 << iota
	CtxPremium
	CtxMarkPx
	CtxOraclePx
	CtxOpenInterest
	CtxDayNtlVlm
	CtxPrevDayPx
)

// CtxAll is the full presence mask: every ctx number was parsed.
const CtxAll = CtxFunding | CtxPremium | CtxMarkPx | CtxOraclePx | CtxOpenInterest | CtxDayNtlVlm | CtxPrevDayPx

// Sentinel errors. Every decoding failure is one of these (or wraps
// ErrUnknownCoin) and is usable with errors.Is.
var (
	// ErrNotThisChannel is returned when the frame's channel does not match
	// the decoder, including non-data frames (pong, _meta, ack).
	ErrNotThisChannel = errors.New("feed: frame is not for this channel")
	// ErrMalformed is returned for any structural or value parse failure:
	// truncated frames, garbage, wrong types, out-of-range numbers,
	// missing required members.
	ErrMalformed = errors.New("feed: malformed frame")
	// ErrTooManyLevels is returned when a book side carries more than the
	// contract's maximum depth of 20 levels: a venue change must surface,
	// never a silent truncation.
	ErrTooManyLevels = errors.New("feed: l2Book side exceeds 20 levels")
	// ErrUnknownCoin is returned (wrapped in UnknownCoinError) when a frame
	// carries a coin outside the decoder's universe.
	ErrUnknownCoin = errors.New("feed: coin not in universe")
)

// UnknownCoinError is the concrete error returned for a coin outside the
// universe; it wraps ErrUnknownCoin and carries the offending coin text
// (allocated only on this error path).
type UnknownCoinError struct {
	Coin string
}

func (e *UnknownCoinError) Error() string { return "feed: unknown coin " + e.Coin }

func (e *UnknownCoinError) Unwrap() error { return ErrUnknownCoin }

// Level is one price level: price, size (base units) and order count n.
type Level struct {
	Px, Sz float64
	N      int32
}

// L2Book is a decoded l2Book frame. Bids and Asks are fixed arrays of 20
// (the contract's maximum depth); NBids/NAsks hold the decoded counts and
// levels beyond them are zeroed so a reused destination cannot leak previous
// frames.
type L2Book struct {
	Coin         string
	TimeMs       int64
	NBids, NAsks int
	Bids, Asks   [20]Level
}

// BBO is a decoded bbo frame. Either side may be absent (one-sided book):
// the level fields are zeroed and the Has flag is false, so zero is never
// presented as observed.
type BBO struct {
	Coin     string
	TimeMs   int64
	HasBid   bool
	HasAsk   bool
	Bid, Ask Level
}

// Trade is one decoded print. Side is SideBuy ('B', taker bought) or
// SideSell ('A', taker sold). Venue-only members (hash, users, ...) are
// skipped and never retained.
type Trade struct {
	Coin        string
	Side        uint8
	Px, Sz      float64
	TimeMs, Tid int64
}

// AssetCtx is a decoded per-asset context frame. All ctx numbers may be
// absent depending on the venue: Have carries one bit per number and a
// number with a clear bit is zeroed and must read as NULL downstream, never
// as 0. MidPx may be absent (HasMidPx false); impact prices may be absent
// (HasImpact false).
type AssetCtx struct {
	Coin                               string
	Have                               CtxFields
	Funding, Premium, MarkPx, OraclePx float64
	OpenInterest, DayNtlVlm, PrevDayPx float64
	MidPx                              float64
	HasMidPx                           bool
	ImpactBid, ImpactAsk               float64
	HasImpact                          bool
}

// Event is one decoded frame, discriminated by Kind. Exactly the member
// selected by Kind is meaningful; the others are stale and must be ignored.
type Event struct {
	Kind   Kind
	L2     L2Book
	BBO    BBO
	Ctx    AssetCtx
	Trades []Trade // reused backing array: Decode appends to Trades[:0]
}

// Peeker classifies a frame and extracts its universe-interned instrument
// without a full decode or allocation. For a structurally readable data frame
// with an unknown coin, ok is true and instrument is empty. Non-data and
// malformed frames return ok=false, retaining the classified kind. Payload
// field validation remains the responsibility of FrameDecoder.
type Peeker interface {
	Peek(frame []byte) (kind Kind, instrument string, ok bool)
}

// FrameDecoder decodes one venue's raw frames into Events.
type FrameDecoder interface {
	// Decode classifies frame and decodes data frames into the matching
	// member of ev, setting ev.Kind. Non-data frames (pong, _meta, ack)
	// return nil with ev.Kind set and no member touched. A frame of no
	// known kind returns ErrNotThisChannel with ev.Kind = KindUnknown.
	// Malformed data returns an error wrapping ErrMalformed (or
	// ErrTooManyLevels / ErrUnknownCoin). Zero allocations on the success
	// path once ev.Trades has capacity; never panics on any input.
	Decode(frame []byte, ev *Event) error
}
