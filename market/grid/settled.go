package grid

// Settled value source for funding_settlement (docs/DATA_POINTS.md 2.3,
// rule R18). The engine treats it as consumer-defined: the replay tests
// feed it from fundingHistory-shaped JSON, a live engine from its fetcher.

// SettledState is the resolution state of one period's settled rate.
type SettledState uint8

const (
	// SettledPending: not known yet; the engine waits for the value or the
	// settled_wait_s deadline (rule R20).
	SettledPending SettledState = iota
	// SettledFound: the settled rate is known (rule R18).
	SettledFound
	// SettledMissing: settled data was supplied for the instrument but has
	// no entry for the period (rule R19 flag 2).
	SettledMissing
	// SettledNotFetched: no settled data at all for the instrument (rule
	// R19 flag 1).
	SettledNotFetched
)

// SettledSource reports the settled funding rate of one instrument for the
// period ending at settleS (UTC seconds). Implementations may return
// SettledPending and answer later (the engine re-asks the head of its
// queue); the map-based implementation below always resolves immediately.
type SettledSource interface {
	Settled(instrument string, settleS int64) (rate float64, st SettledState)
}

// SettledPoint is one fundingHistory entry: a venue timestamp in ms and the
// settled rate of the period it stamps.
type SettledPoint struct {
	TimeMs int64
	Rate   float64
}

// mapSettled is the SettledSource over a per-instrument point list
// (the fundingHistory shape).
type mapSettled map[string][]SettledPoint

// NewMapSettled builds a SettledSource over a per-instrument list of
// fundingHistory entries. Rule R18: the entry for period S is the first
// with S*1000 <= time < S*1000+60000 (the venue stamps it a few ms after
// S). An instrument missing from the map is SettledNotFetched; present but
// without a matching entry is SettledMissing. Both resolve immediately.
func NewMapSettled(m map[string][]SettledPoint) SettledSource {
	return mapSettled(m)
}

// Settled implements SettledSource (rule R18, first match in list order).
func (m mapSettled) Settled(instrument string, settleS int64) (float64, SettledState) {
	pts, ok := m[instrument]
	if !ok {
		return 0, SettledNotFetched
	}
	lo := settleS * 1000
	hi := lo + 60000
	for i := range pts {
		if pts[i].TimeMs >= lo && pts[i].TimeMs < hi {
			return pts[i].Rate, SettledFound
		}
	}
	return 0, SettledMissing
}
