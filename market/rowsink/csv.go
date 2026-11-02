package rowsink

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// CSVSink writes market_state_1s and trade_flow_1s rows in the rule R15 CSV
// format to two writers. It implements Sink. Errors are sticky: the Sink
// methods have no return value, so the first error is remembered, later
// writes become no-ops, and Flush/Err report it. Steady-state writes
// allocate nothing per row (reusable line buffer, buffered writers).
//
// Format (rule R15): header = Columns joined by ','; one line per row with
// '\n' line ends; no quoting (an instrument containing ',', '"', '\n' or
// '\r' is an error); nulls are empty fields; bools "true"/"false"; ints
// plain decimal; floats via AppendFloat (Python repr); MidSrc via String().
type CSVSink struct {
	ms, tf *bufio.Writer
	fn     *bufio.Writer // funding_settlement, enabled by WithFunding
	line   []byte
	err    error
	layout Layout
}

// NewCSVSink builds a sink writing market state rows to marketState and
// trade flow rows to tradeFlow with the given layout (rule R27; none or the
// zero Layout = DefaultLayout()): the headers are the layout's
// MarketStateColumns / TradeFlowColumns and only the first
// len(DepthBandsBps) / len(LadderEdgesBps) array entries of a row are
// written. Both headers are written immediately; an invalid layout or a
// header write error is sticky and reported by Flush/Err.
func NewCSVSink(marketState, tradeFlow io.Writer, layouts ...Layout) *CSVSink {
	s := &CSVSink{
		ms:     bufio.NewWriter(marketState),
		layout: selectedLayout(layouts),
		line:   make([]byte, 0, 8192),
		tf:     bufio.NewWriter(tradeFlow),
	}
	s.setErr(s.layout.Validate())
	s.writeHeader(s.ms, s.layout.MarketStateColumns())
	s.writeHeader(s.tf, s.layout.TradeFlowColumns())
	return s
}

// WithFunding enables funding_settlement output to w and writes its header
// immediately (like the other tables). It returns the receiver. Without
// WithFunding the sink writes only the two existing tables and a
// FundingSettlement call is a sticky error. The sink then implements
// FundingSink.
func (s *CSVSink) WithFunding(w io.Writer) *CSVSink {
	s.fn = bufio.NewWriter(w)
	s.writeHeader(s.fn, FundingSettlementColumns)
	return s
}

func (s *CSVSink) writeHeader(w *bufio.Writer, columns []string) {
	if s.err != nil {
		return
	}
	line := strings.Join(columns, ",") + "\n"
	if _, err := w.WriteString(line); err != nil {
		s.setErr(err)
	}
}

func (s *CSVSink) setErr(err error) {
	if s.err == nil {
		s.err = err
	}
}

// Err returns the first error seen (sticky), or nil.
func (s *CSVSink) Err() error { return s.err }

// Flush flushes all buffered writers and returns the first error seen.
func (s *CSVSink) Flush() error {
	if err := s.ms.Flush(); err != nil {
		s.setErr(err)
	}
	if err := s.tf.Flush(); err != nil {
		s.setErr(err)
	}
	if s.fn != nil {
		if err := s.fn.Flush(); err != nil {
			s.setErr(err)
		}
	}
	return s.err
}

// MarketState writes one market_state_1s row (Sink).
func (s *CSVSink) MarketState(r *MarketState) {
	if s.err != nil {
		return
	}
	if !validInstrument(r.Instrument) {
		s.setErr(fmt.Errorf("rowsink: instrument %q contains a reserved CSV character", r.Instrument))
		return
	}
	b := s.line[:0]
	b = strconv.AppendInt(b, r.TsS, 10)
	b = append(b, ',')
	b = append(b, r.Instrument...)
	b = append(b, ',')
	b = appendF64(b, r.BidPx)
	b = append(b, ',')
	b = appendF64(b, r.AskPx)
	b = append(b, ',')
	b = appendF64(b, r.BidSz)
	b = append(b, ',')
	b = appendF64(b, r.AskSz)
	b = append(b, ',')
	b = appendI64(b, r.BidN)
	b = append(b, ',')
	b = appendI64(b, r.AskN)
	b = append(b, ',')
	b = appendF64(b, r.MidPx)
	b = append(b, ',')
	b = append(b, r.MidSrc.String()...)
	b = append(b, ',')
	b = appendF64(b, r.MidPxHigh)
	b = append(b, ',')
	b = appendF64(b, r.MidPxLow)
	b = append(b, ',')
	b = strconv.AppendInt(b, r.BboUpdates, 10)
	b = append(b, ',')
	b = appendF64(b, r.SpreadBps)
	b = append(b, ',')
	b = appendF64(b, r.MicroPx)
	b = append(b, ',')
	b = appendF64(b, r.MicroMinusMidBps)
	b = append(b, ',')
	b = appendF64(b, r.ImbalanceL1)
	for i := range s.layout.DepthBandsBps {
		b = append(b, ',')
		b = appendF64(b, r.DepthBid[i])
	}
	for i := range s.layout.DepthBandsBps {
		b = append(b, ',')
		b = appendF64(b, r.DepthAsk[i])
	}
	b = append(b, ',')
	b = appendF64(b, r.L2ReachBid)
	b = append(b, ',')
	b = appendF64(b, r.L2ReachAsk)
	b = append(b, ',')
	b = appendI64(b, r.L2LevelsBid)
	b = append(b, ',')
	b = appendI64(b, r.L2LevelsAsk)
	b = append(b, ',')
	b = appendI64(b, r.BboAgeMs)
	b = append(b, ',')
	b = appendI64(b, r.L2AgeMs)
	b = append(b, ',')
	b = appendI64(b, r.CtxAgeMs)
	b = append(b, ',')
	b = appendI64(b, r.LastBboMs)
	b = append(b, ',')
	b = appendI64(b, r.LastL2Ms)
	b = append(b, ',')
	b = appendI64(b, r.LastCtxMs)
	b = append(b, ',')
	b = appendF64(b, r.MarkPx)
	b = append(b, ',')
	b = appendF64(b, r.OraclePx)
	b = append(b, ',')
	b = appendF64(b, r.FundingRate)
	b = append(b, ',')
	b = appendF64(b, r.Premium)
	b = append(b, ',')
	b = appendF64(b, r.OpenInterest)
	b = append(b, ',')
	b = appendF64(b, r.OpenInterestUSD)
	b = append(b, ',')
	b = appendF64(b, r.ImpactBidPx)
	b = append(b, ',')
	b = appendF64(b, r.ImpactAskPx)
	b = append(b, ',')
	b = appendF64(b, r.DayNtlVlmUSD)
	b = append(b, ',')
	b = appendF64(b, r.MarkOracleBps)
	b = append(b, ',')
	b = appendF64(b, r.MidOracleBps)
	b = append(b, ',')
	b = appendI64(b, r.FundingIntervalS)
	b = append(b, ',')
	b = appendI64(b, r.TimeToSettleS)
	b = append(b, ',')
	b = appendBoolNN(b, r.BboValid)
	b = append(b, ',')
	b = appendBoolNN(b, r.BookValid)
	b = append(b, ',')
	b = appendBoolNN(b, r.CtxValid)
	b = append(b, ',')
	b = appendBoolNN(b, r.Valid)
	b = append(b, ',')
	b = appendBoolNN(b, r.FeedGap)
	b = append(b, ',')
	b = appendBoolNN(b, r.EngineOverflow)
	b = append(b, ',')
	b = strconv.AppendUint(b, uint64(r.Quality), 10)
	b = append(b, '\n')
	s.line = b
	if _, err := s.ms.Write(b); err != nil {
		s.setErr(err)
	}
}
func (s *CSVSink) TradeFlow(r *TradeFlow) {
	if s.err != nil {
		return
	}
	if !validInstrument(r.Instrument) {
		s.setErr(fmt.Errorf("rowsink: instrument %q contains a reserved CSV character", r.Instrument))
		return
	}
	b := s.line[:0]
	b = strconv.AppendInt(b, r.TsS, 10)
	b = append(b, ',')
	b = append(b, r.Instrument...)
	b = append(b, ',')
	b = strconv.AppendInt(b, r.BuyEvents, 10)
	b = append(b, ',')
	b = strconv.AppendInt(b, r.SellEvents, 10)
	b = append(b, ',')
	b = AppendFloat(b, r.BuyUSD)
	b = append(b, ',')
	b = AppendFloat(b, r.SellUSD)
	b = append(b, ',')
	b = AppendFloat(b, r.BuyQty)
	b = append(b, ',')
	b = AppendFloat(b, r.SellQty)
	b = append(b, ',')
	b = AppendFloat(b, r.MaxTradeUSD)
	b = append(b, ',')
	b = AppendFloat(b, r.SumNotionalSq)
	b = append(b, ',')
	b = appendF64(b, r.Vwap)
	b = append(b, ',')
	b = appendI64(b, r.FirstTradeMs)
	b = append(b, ',')
	b = appendI64(b, r.LastTradeMs)
	b = append(b, ',')
	b = appendF64(b, r.LastTradePx)
	b = append(b, ',')
	b = appendF64(b, r.FlowImbalance)
	b = append(b, ',')
	b = strconv.AppendInt(b, r.DupDropped, 10)
	b = append(b, ',')
	b = appendBoolNN(b, r.TradeFeedValid)
	b = append(b, ',')
	b = appendF64(b, r.SellPxMin)
	b = append(b, ',')
	b = appendF64(b, r.BuyPxMax)
	b = append(b, ',')
	b = AppendFloat(b, r.SellQtyAtBid)
	b = append(b, ',')
	b = AppendFloat(b, r.SellQtyThruBid)
	b = append(b, ',')
	b = AppendFloat(b, r.BuyQtyAtAsk)
	b = append(b, ',')
	b = AppendFloat(b, r.BuyQtyThruAsk)
	b = append(b, ',')
	b = AppendFloat(b, r.SellQtyUnclassified)
	b = append(b, ',')
	b = AppendFloat(b, r.BuyQtyUnclassified)
	b = append(b, ',')
	b = AppendFloat(b, r.BidL1Removed)
	b = append(b, ',')
	b = AppendFloat(b, r.BidL1Added)
	b = append(b, ',')
	b = AppendFloat(b, r.AskL1Removed)
	b = append(b, ',')
	b = AppendFloat(b, r.AskL1Added)
	b = append(b, ',')
	b = strconv.AppendInt(b, r.BidTouchChanges, 10)
	b = append(b, ',')
	b = strconv.AppendInt(b, r.AskTouchChanges, 10)
	b = append(b, ',')
	b = strconv.AppendInt(b, r.FlowAttribFlags, 10)
	for i := range s.layout.LadderEdgesBps {
		b = append(b, ',')
		b = appendF64(b, r.SellQtyGe[i])
	}
	for i := range s.layout.LadderEdgesBps {
		b = append(b, ',')
		b = appendF64(b, r.BuyQtyGe[i])
	}
	b = append(b, '\n')
	s.line = b
	if _, err := s.tf.Write(b); err != nil {
		s.setErr(err)
	}
}

// FundingSettlement writes one funding_settlement row (FundingSink). The
// output must have been enabled with WithFunding; without it the call is a
// sticky error.
func (s *CSVSink) FundingSettlement(r *FundingSettlement) {
	if s.err != nil {
		return
	}
	if s.fn == nil {
		s.setErr(errors.New("rowsink: funding output not enabled (call WithFunding)"))
		return
	}
	if !validInstrument(r.Instrument) {
		s.setErr(fmt.Errorf("rowsink: instrument %q contains a reserved CSV character", r.Instrument))
		return
	}
	b := s.line[:0]
	b = strconv.AppendInt(b, r.TsS, 10)
	b = append(b, ',')
	b = append(b, r.Instrument...)
	b = append(b, ',')
	b = strconv.AppendInt(b, r.FundingIntervalS, 10)
	b = append(b, ',')
	b = appendF64(b, r.FundingSettled)
	b = append(b, ',')
	b = appendF64(b, r.FundingCtxLast)
	b = append(b, ',')
	b = appendF64(b, r.FundingCtxMean)
	b = append(b, ',')
	b = appendF64(b, r.FundingCtxMin)
	b = append(b, ',')
	b = appendF64(b, r.FundingCtxMax)
	b = append(b, ',')
	b = strconv.AppendInt(b, r.FundingChanges, 10)
	b = append(b, ',')
	b = appendF64(b, r.PremiumMean)
	b = append(b, ',')
	b = appendF64(b, r.PremiumLast)
	b = append(b, ',')
	b = appendF64(b, r.OraclePxSettle)
	b = append(b, ',')
	b = appendF64(b, r.MarkPxSettle)
	b = append(b, ',')
	b = appendF64(b, r.OIUSDStart)
	b = append(b, ',')
	b = appendF64(b, r.OIUSDEnd)
	b = append(b, ',')
	b = strconv.AppendInt(b, r.CtxFrames, 10)
	b = append(b, ',')
	b = appendI64(b, r.CtxGapMaxMs)
	b = append(b, ',')
	b = appendF64(b, r.ReconcileDiff)
	b = append(b, ',')
	b = strconv.AppendUint(b, uint64(r.SourceFlags), 10)
	b = append(b, '\n')
	s.line = b
	if _, err := s.fn.Write(b); err != nil {
		s.setErr(err)
	}
}

// validInstrument reports whether s is usable as an unquoted CSV field.
func validInstrument(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ',', '"', '\n', '\r':
			return false
		}
	}
	return true
}

func appendF64(b []byte, v F64) []byte {
	if v.Ok {
		b = AppendFloat(b, v.V)
	}
	return b
}

func appendI64(b []byte, v I64) []byte {
	if v.Ok {
		b = strconv.AppendInt(b, v.V, 10)
	}
	return b
}

func appendBool(b []byte, v Bool) []byte {
	if v.Ok {
		b = appendBoolNN(b, v.V)
	}
	return b
}

func appendBoolNN(b []byte, v bool) []byte {
	if v {
		return append(b, "true"...)
	}
	return append(b, "false"...)
}

// ReadMarketStateCSV parses an R15 market_state_1s CSV written with the given
// layout (none or the zero Layout = DefaultLayout()). The header must name
// exactly the layout's MarketStateColumns (67 for the default layout), each
// once; cells are located by header name. Every data line must have that many
// fields; empty fields are null for nullable columns and an error for
// non-nullable ones. Errors carry the line number (header is line 1).
func ReadMarketStateCSV(r io.Reader, layouts ...Layout) ([]MarketState, error) {
	layout := selectedLayout(layouts)
	if err := layout.Validate(); err != nil {
		return nil, err
	}
	cols := layout.MarketStateColumns()
	expected := strings.Join(cols, ",")
	index := newColIndex(cols)
	var rows []MarketState
	err := readCSV(r, expected, len(cols), func(lineNo int, fields []string) error {
		var row MarketState
		if err := parseMarketState(index, fields, &row, layout); err != nil {
			return fmt.Errorf("line %d: %w", lineNo, err)
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("rowsink: market_state_1s: %w", err)
	}
	return rows, nil
}

// ReadTradeFlowCSV parses an R15 trade_flow_1s CSV (see ReadMarketStateCSV).
func ReadTradeFlowCSV(r io.Reader, layouts ...Layout) ([]TradeFlow, error) {
	layout := selectedLayout(layouts)
	if err := layout.Validate(); err != nil {
		return nil, err
	}
	cols := layout.TradeFlowColumns()
	expected := strings.Join(cols, ",")
	index := newColIndex(cols)
	var rows []TradeFlow
	err := readCSV(r, expected, len(cols), func(lineNo int, fields []string) error {
		var row TradeFlow
		if err := parseTradeFlow(index, fields, &row, layout); err != nil {
			return fmt.Errorf("line %d: %w", lineNo, err)
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("rowsink: trade_flow_1s: %w", err)
	}
	return rows, nil
}

// readCSV checks the header and feeds every parsed field slice to handle.
func readCSV(r io.Reader, header string, nfields int, handle func(lineNo int, fields []string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	var order []int
	var ordered []string
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if lineNo == 1 {
			names := strings.Split(line, ",")
			if len(names) != nfields {
				return fmt.Errorf("header mismatch: got %q, want %q", line, header)
			}
			index := make(map[string]int, len(names))
			for i, n := range names {
				if _, ok := index[n]; ok {
					return fmt.Errorf("duplicate column %q", n)
				}
				index[n] = i
			}
			order = make([]int, nfields)
			ordered = make([]string, nfields)
			for i, n := range strings.Split(header, ",") {
				j, ok := index[n]
				if !ok {
					return fmt.Errorf("missing column %q", n)
				}
				order[i] = j
			}
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) != nfields {
			return fmt.Errorf("line %d: got %d fields, want %d", lineNo, len(fields), nfields)
		}
		for i, j := range order {
			ordered[i] = fields[j]
		}
		if err := handle(lineNo, ordered); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if lineNo == 0 {
		return errors.New("missing header")
	}
	return nil
}

// Per-type field parsers. Non-nullable columns reject empty fields; the
// nullable ones map empty to Ok=false. Names come from the Columns slices.

func parseIntNN(name, s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty non-nullable %s", name)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad %s %q: %w", name, s, err)
	}
	return v, nil
}

func parseIntOpt(name, s string, dst *I64) error {
	if s == "" {
		*dst = I64{}
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("bad %s %q: %w", name, s, err)
	}
	*dst = I64{V: v, Ok: true}
	return nil
}

func parseFloatNN(name, s string) (float64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty non-nullable %s", name)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("bad %s %q: %w", name, s, err)
	}
	return v, nil
}

func parseFloatOpt(name, s string, dst *F64) error {
	if s == "" {
		*dst = F64{}
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("bad %s %q: %w", name, s, err)
	}
	*dst = F64{V: v, Ok: true}
	return nil
}

func parseBoolNN(name, s string) (bool, error) {
	switch s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "":
		return false, fmt.Errorf("empty non-nullable %s", name)
	default:
		return false, fmt.Errorf("bad %s %q: want true or false", name, s)
	}
}

func parseBoolOpt(name, s string, dst *Bool) error {
	switch s {
	case "":
		*dst = Bool{}
		return nil
	case "true":
		*dst = Bool{V: true, Ok: true}
		return nil
	case "false":
		*dst = Bool{V: false, Ok: true}
		return nil
	default:
		return fmt.Errorf("bad %s %q: want true or false", name, s)
	}
}

func parseStrNN(name, s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("empty non-nullable %s", name)
	}
	if !validInstrument(s) {
		return "", fmt.Errorf("bad %s %q: reserved CSV character", name, s)
	}
	return s, nil
}

func parseUintNN(name, s string) (uint16, error) {
	if s == "" {
		return 0, fmt.Errorf("empty non-nullable %s", name)
	}
	v, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("bad %s %q: %w", name, s, err)
	}
	return uint16(v), nil
}

func parseMidSrc(name, s string) (MidSrc, error) {
	switch s {
	case "none":
		return MidNone, nil
	case "bbo":
		return MidBBO, nil
	case "l2":
		return MidL2, nil
	case "":
		return 0, fmt.Errorf("empty non-nullable %s", name)
	default:
		return 0, fmt.Errorf("bad %s %q: want none, bbo or l2", name, s)
	}
}

// colIndex maps a column name of the run's layout builder list to its
// position in the reordered field slice handed to the parsers (readCSV puts
// the file's cells into builder order by header name), so every cell is
// addressed by name and a column change cannot silently shift cells.
type colIndex map[string]int

func newColIndex(cols []string) colIndex {
	c := make(colIndex, len(cols))
	for i, n := range cols {
		c[n] = i
	}
	return c
}

// at returns the cell of column name. An unknown name is a programming error
// (a parser asking for a column the builder does not produce) and panics, so
// any test that reads a row exposes it instead of silently reading cell 0.
func (c colIndex) at(f []string, name string) string {
	i, ok := c[name]
	if !ok {
		panic("rowsink: parser asks for unknown column " + name)
	}
	return f[i]
}

// parseMarketState parses one line (already in the layout's
// MarketStateColumns order), addressing every cell by column name.
func parseMarketState(index colIndex, f []string, row *MarketState, layout Layout) error {
	var err error
	if row.TsS, err = parseIntNN("ts_s", index.at(f, "ts_s")); err != nil {
		return err
	}
	if row.Instrument, err = parseStrNN("instrument", index.at(f, "instrument")); err != nil {
		return err
	}
	if err = parseFloatOpt("bid_px", index.at(f, "bid_px"), &row.BidPx); err != nil {
		return err
	}
	if err = parseFloatOpt("ask_px", index.at(f, "ask_px"), &row.AskPx); err != nil {
		return err
	}
	if err = parseFloatOpt("bid_sz", index.at(f, "bid_sz"), &row.BidSz); err != nil {
		return err
	}
	if err = parseFloatOpt("ask_sz", index.at(f, "ask_sz"), &row.AskSz); err != nil {
		return err
	}
	if err = parseIntOpt("bid_n", index.at(f, "bid_n"), &row.BidN); err != nil {
		return err
	}
	if err = parseIntOpt("ask_n", index.at(f, "ask_n"), &row.AskN); err != nil {
		return err
	}
	if err = parseFloatOpt("mid_px", index.at(f, "mid_px"), &row.MidPx); err != nil {
		return err
	}
	if row.MidSrc, err = parseMidSrc("mid_src", index.at(f, "mid_src")); err != nil {
		return err
	}
	if err = parseFloatOpt("mid_px_high", index.at(f, "mid_px_high"), &row.MidPxHigh); err != nil {
		return err
	}
	if err = parseFloatOpt("mid_px_low", index.at(f, "mid_px_low"), &row.MidPxLow); err != nil {
		return err
	}
	if row.BboUpdates, err = parseIntNN("bbo_updates", index.at(f, "bbo_updates")); err != nil {
		return err
	}
	if err = parseFloatOpt("spread_bps", index.at(f, "spread_bps"), &row.SpreadBps); err != nil {
		return err
	}
	if err = parseFloatOpt("micro_px", index.at(f, "micro_px"), &row.MicroPx); err != nil {
		return err
	}
	if err = parseFloatOpt("micro_minus_mid_bps", index.at(f, "micro_minus_mid_bps"), &row.MicroMinusMidBps); err != nil {
		return err
	}
	if err = parseFloatOpt("imbalance_l1", index.at(f, "imbalance_l1"), &row.ImbalanceL1); err != nil {
		return err
	}
	for i, e := range layout.DepthBandsBps {
		name := "depth_bid_" + BandSuffix(e) + "bps_usd"
		if err = parseFloatOpt(name, index.at(f, name), &row.DepthBid[i]); err != nil {
			return err
		}
	}
	for i, e := range layout.DepthBandsBps {
		name := "depth_ask_" + BandSuffix(e) + "bps_usd"
		if err = parseFloatOpt(name, index.at(f, name), &row.DepthAsk[i]); err != nil {
			return err
		}
	}
	if err = parseFloatOpt("l2_reach_bid_bps", index.at(f, "l2_reach_bid_bps"), &row.L2ReachBid); err != nil {
		return err
	}
	if err = parseFloatOpt("l2_reach_ask_bps", index.at(f, "l2_reach_ask_bps"), &row.L2ReachAsk); err != nil {
		return err
	}
	if err = parseIntOpt("l2_levels_bid", index.at(f, "l2_levels_bid"), &row.L2LevelsBid); err != nil {
		return err
	}
	if err = parseIntOpt("l2_levels_ask", index.at(f, "l2_levels_ask"), &row.L2LevelsAsk); err != nil {
		return err
	}
	if err = parseIntOpt("bbo_age_ms", index.at(f, "bbo_age_ms"), &row.BboAgeMs); err != nil {
		return err
	}
	if err = parseIntOpt("l2_age_ms", index.at(f, "l2_age_ms"), &row.L2AgeMs); err != nil {
		return err
	}
	if err = parseIntOpt("ctx_age_ms", index.at(f, "ctx_age_ms"), &row.CtxAgeMs); err != nil {
		return err
	}
	if err = parseIntOpt("last_bbo_ms", index.at(f, "last_bbo_ms"), &row.LastBboMs); err != nil {
		return err
	}
	if err = parseIntOpt("last_l2_ms", index.at(f, "last_l2_ms"), &row.LastL2Ms); err != nil {
		return err
	}
	if err = parseIntOpt("last_ctx_ms", index.at(f, "last_ctx_ms"), &row.LastCtxMs); err != nil {
		return err
	}
	if err = parseFloatOpt("mark_px", index.at(f, "mark_px"), &row.MarkPx); err != nil {
		return err
	}
	if err = parseFloatOpt("oracle_px", index.at(f, "oracle_px"), &row.OraclePx); err != nil {
		return err
	}
	if err = parseFloatOpt("funding_rate", index.at(f, "funding_rate"), &row.FundingRate); err != nil {
		return err
	}
	if err = parseFloatOpt("premium", index.at(f, "premium"), &row.Premium); err != nil {
		return err
	}
	if err = parseFloatOpt("open_interest", index.at(f, "open_interest"), &row.OpenInterest); err != nil {
		return err
	}
	if err = parseFloatOpt("open_interest_usd", index.at(f, "open_interest_usd"), &row.OpenInterestUSD); err != nil {
		return err
	}
	if err = parseFloatOpt("impact_bid_px", index.at(f, "impact_bid_px"), &row.ImpactBidPx); err != nil {
		return err
	}
	if err = parseFloatOpt("impact_ask_px", index.at(f, "impact_ask_px"), &row.ImpactAskPx); err != nil {
		return err
	}
	if err = parseFloatOpt("day_ntl_vlm_usd", index.at(f, "day_ntl_vlm_usd"), &row.DayNtlVlmUSD); err != nil {
		return err
	}
	if err = parseFloatOpt("mark_oracle_bps", index.at(f, "mark_oracle_bps"), &row.MarkOracleBps); err != nil {
		return err
	}
	if err = parseFloatOpt("mid_oracle_bps", index.at(f, "mid_oracle_bps"), &row.MidOracleBps); err != nil {
		return err
	}
	if err = parseIntOpt("funding_interval_s", index.at(f, "funding_interval_s"), &row.FundingIntervalS); err != nil {
		return err
	}
	if err = parseIntOpt("time_to_settle_s", index.at(f, "time_to_settle_s"), &row.TimeToSettleS); err != nil {
		return err
	}
	if row.BboValid, err = parseBoolNN("bbo_valid", index.at(f, "bbo_valid")); err != nil {
		return err
	}
	if row.BookValid, err = parseBoolNN("book_valid", index.at(f, "book_valid")); err != nil {
		return err
	}
	if row.CtxValid, err = parseBoolNN("ctx_valid", index.at(f, "ctx_valid")); err != nil {
		return err
	}
	if row.Valid, err = parseBoolNN("valid", index.at(f, "valid")); err != nil {
		return err
	}
	if row.FeedGap, err = parseBoolNN("feed_gap", index.at(f, "feed_gap")); err != nil {
		return err
	}
	if row.EngineOverflow, err = parseBoolNN("engine_overflow", index.at(f, "engine_overflow")); err != nil {
		return err
	}
	if row.Quality, err = parseUintNN("quality", index.at(f, "quality")); err != nil {
		return err
	}
	return nil
}

// parseTradeFlow parses one line (already in the layout's TradeFlowColumns
// order), addressing every cell by column name.
func parseTradeFlow(index colIndex, f []string, row *TradeFlow, layout Layout) error {
	var err error
	if row.TsS, err = parseIntNN("ts_s", index.at(f, "ts_s")); err != nil {
		return err
	}
	if row.Instrument, err = parseStrNN("instrument", index.at(f, "instrument")); err != nil {
		return err
	}
	if row.BuyEvents, err = parseIntNN("buy_events", index.at(f, "buy_events")); err != nil {
		return err
	}
	if row.SellEvents, err = parseIntNN("sell_events", index.at(f, "sell_events")); err != nil {
		return err
	}
	if row.BuyUSD, err = parseFloatNN("buy_usd", index.at(f, "buy_usd")); err != nil {
		return err
	}
	if row.SellUSD, err = parseFloatNN("sell_usd", index.at(f, "sell_usd")); err != nil {
		return err
	}
	if row.BuyQty, err = parseFloatNN("buy_qty", index.at(f, "buy_qty")); err != nil {
		return err
	}
	if row.SellQty, err = parseFloatNN("sell_qty", index.at(f, "sell_qty")); err != nil {
		return err
	}
	if row.MaxTradeUSD, err = parseFloatNN("max_trade_usd", index.at(f, "max_trade_usd")); err != nil {
		return err
	}
	if row.SumNotionalSq, err = parseFloatNN("sum_notional_sq", index.at(f, "sum_notional_sq")); err != nil {
		return err
	}
	if err = parseFloatOpt("vwap", index.at(f, "vwap"), &row.Vwap); err != nil {
		return err
	}
	if err = parseIntOpt("first_trade_ms", index.at(f, "first_trade_ms"), &row.FirstTradeMs); err != nil {
		return err
	}
	if err = parseIntOpt("last_trade_ms", index.at(f, "last_trade_ms"), &row.LastTradeMs); err != nil {
		return err
	}
	if err = parseFloatOpt("last_trade_px", index.at(f, "last_trade_px"), &row.LastTradePx); err != nil {
		return err
	}
	if err = parseFloatOpt("flow_imbalance", index.at(f, "flow_imbalance"), &row.FlowImbalance); err != nil {
		return err
	}
	if row.DupDropped, err = parseIntNN("dup_dropped", index.at(f, "dup_dropped")); err != nil {
		return err
	}
	if row.TradeFeedValid, err = parseBoolNN("trade_feed_valid", index.at(f, "trade_feed_valid")); err != nil {
		return err
	}
	if err = parseFloatOpt("sell_px_min", index.at(f, "sell_px_min"), &row.SellPxMin); err != nil {
		return err
	}
	if err = parseFloatOpt("buy_px_max", index.at(f, "buy_px_max"), &row.BuyPxMax); err != nil {
		return err
	}
	if row.SellQtyAtBid, err = parseFloatNN("sell_qty_at_bid", index.at(f, "sell_qty_at_bid")); err != nil {
		return err
	}
	if row.SellQtyThruBid, err = parseFloatNN("sell_qty_thru_bid", index.at(f, "sell_qty_thru_bid")); err != nil {
		return err
	}
	if row.BuyQtyAtAsk, err = parseFloatNN("buy_qty_at_ask", index.at(f, "buy_qty_at_ask")); err != nil {
		return err
	}
	if row.BuyQtyThruAsk, err = parseFloatNN("buy_qty_thru_ask", index.at(f, "buy_qty_thru_ask")); err != nil {
		return err
	}
	if row.SellQtyUnclassified, err = parseFloatNN("sell_qty_unclassified", index.at(f, "sell_qty_unclassified")); err != nil {
		return err
	}
	if row.BuyQtyUnclassified, err = parseFloatNN("buy_qty_unclassified", index.at(f, "buy_qty_unclassified")); err != nil {
		return err
	}
	if row.BidL1Removed, err = parseFloatNN("bid_l1_removed_qty", index.at(f, "bid_l1_removed_qty")); err != nil {
		return err
	}
	if row.BidL1Added, err = parseFloatNN("bid_l1_added_qty", index.at(f, "bid_l1_added_qty")); err != nil {
		return err
	}
	if row.AskL1Removed, err = parseFloatNN("ask_l1_removed_qty", index.at(f, "ask_l1_removed_qty")); err != nil {
		return err
	}
	if row.AskL1Added, err = parseFloatNN("ask_l1_added_qty", index.at(f, "ask_l1_added_qty")); err != nil {
		return err
	}
	if row.BidTouchChanges, err = parseIntNN("bid_touch_changes", index.at(f, "bid_touch_changes")); err != nil {
		return err
	}
	if row.AskTouchChanges, err = parseIntNN("ask_touch_changes", index.at(f, "ask_touch_changes")); err != nil {
		return err
	}
	if row.FlowAttribFlags, err = parseIntNN("flow_attrib_flags", index.at(f, "flow_attrib_flags")); err != nil {
		return err
	}
	for i, e := range layout.LadderEdgesBps {
		name := "sell_qty_ge_" + BandSuffix(e) + "bps"
		if err = parseFloatOpt(name, index.at(f, name), &row.SellQtyGe[i]); err != nil {
			return err
		}
	}
	for i, e := range layout.LadderEdgesBps {
		name := "buy_qty_ge_" + BandSuffix(e) + "bps"
		if err = parseFloatOpt(name, index.at(f, name), &row.BuyQtyGe[i]); err != nil {
			return err
		}
	}
	return nil
}

// ReadFundingSettlementCSV parses an R15 funding_settlement CSV (see
// ReadMarketStateCSV). 19 fields per line; nullable columns:
// funding_settled, funding_ctx_*, premium_*, oracle_px_settle,
// mark_px_settle, oi_usd_*, ctx_gap_max_ms, reconcile_diff.
func ReadFundingSettlementCSV(r io.Reader) ([]FundingSettlement, error) {
	expected := strings.Join(FundingSettlementColumns, ",")
	cols := FundingSettlementColumns
	var rows []FundingSettlement
	err := readCSV(r, expected, len(cols), func(lineNo int, fields []string) error {
		var row FundingSettlement
		if err := parseFundingSettlement(cols, fields, &row); err != nil {
			return fmt.Errorf("line %d: %w", lineNo, err)
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("rowsink: funding_settlement: %w", err)
	}
	return rows, nil
}

// parseFundingSettlement parses one 19-field line, field by field in
// FundingSettlementColumns order.
func parseFundingSettlement(cols, f []string, row *FundingSettlement) error {
	var err error
	if row.TsS, err = parseIntNN(cols[0], f[0]); err != nil {
		return err
	}
	if row.Instrument, err = parseStrNN(cols[1], f[1]); err != nil {
		return err
	}
	if row.FundingIntervalS, err = parseIntNN(cols[2], f[2]); err != nil {
		return err
	}
	if err = parseFloatOpt(cols[3], f[3], &row.FundingSettled); err != nil {
		return err
	}
	if err = parseFloatOpt(cols[4], f[4], &row.FundingCtxLast); err != nil {
		return err
	}
	if err = parseFloatOpt(cols[5], f[5], &row.FundingCtxMean); err != nil {
		return err
	}
	if err = parseFloatOpt(cols[6], f[6], &row.FundingCtxMin); err != nil {
		return err
	}
	if err = parseFloatOpt(cols[7], f[7], &row.FundingCtxMax); err != nil {
		return err
	}
	if row.FundingChanges, err = parseIntNN(cols[8], f[8]); err != nil {
		return err
	}
	if err = parseFloatOpt(cols[9], f[9], &row.PremiumMean); err != nil {
		return err
	}
	if err = parseFloatOpt(cols[10], f[10], &row.PremiumLast); err != nil {
		return err
	}
	if err = parseFloatOpt(cols[11], f[11], &row.OraclePxSettle); err != nil {
		return err
	}
	if err = parseFloatOpt(cols[12], f[12], &row.MarkPxSettle); err != nil {
		return err
	}
	if err = parseFloatOpt(cols[13], f[13], &row.OIUSDStart); err != nil {
		return err
	}
	if err = parseFloatOpt(cols[14], f[14], &row.OIUSDEnd); err != nil {
		return err
	}
	if row.CtxFrames, err = parseIntNN(cols[15], f[15]); err != nil {
		return err
	}
	if err = parseIntOpt(cols[16], f[16], &row.CtxGapMaxMs); err != nil {
		return err
	}
	if err = parseFloatOpt(cols[17], f[17], &row.ReconcileDiff); err != nil {
		return err
	}
	if row.SourceFlags, err = parseUintNN(cols[18], f[18]); err != nil {
		return err
	}
	return nil
}
