package focus

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
)

// Offer and trade assertions read what a transaction did to the order book:
// the OfferEntry a manage-offer operation left behind, and the matches it
// made against offers that were already there.
//
// The two are asserted separately because the protocol records them
// separately, and because an offer that matched completely never becomes an
// entry at all. A manage-sell-offer that crosses the spread is filled on the
// spot: what reaches the ledger is the remainder, or nothing. So "the offer
// was created" and "the trade happened" are different claims about the same
// operation, and a test that means the second one cannot check the first.
//
// The offer id is the other reason these read the ledger rather than the
// envelope. A new offer is submitted with id 0 and the protocol assigns the
// real one, so the id a test can assert on exists only in the result and the
// meta.

// Offer describes an expected offer in the order book.
//
// A zero field is not checked, which is what makes the common assertions
// short: a test that cares only that an offer selling USDC for XLM exists
// leaves the id and the price alone, and a test that knows the id pins it.
// The one exception is the pair of assets, which is checked whenever it is
// given — an offer is a claim about two assets, and a test that names neither
// is not saying much.
type Offer struct {
	// Seller is the account that placed the offer. It accepts a strkey
	// address, an xdr.AccountId or an xdr.MuxedAccount. Nil matches any
	// seller.
	Seller Value
	// OfferID is the offer's ledger id. Zero matches any id, which is the
	// usual case: the protocol assigns the id, so a test rarely knows it in
	// advance.
	OfferID int64
	// Selling is the asset the offer gives up, and Buying the asset it wants.
	// Both accept the forms AssertBalanceChanged accepts; nil matches any
	// asset.
	Selling Value
	Buying  Value
	// Amount is the amount of Selling the offer still has on the book, in
	// stroops. Zero skips the check, since an offer with nothing left on it
	// is not in the ledger to be found.
	Amount int64
	// Price is the offer's price as the protocol stores it: units of Buying
	// per unit of Selling. It accepts "N/D" as a string or an xdr.Price, and
	// is compared as a ratio, so "2/4" and "1/2" are the same price. Nil
	// skips the check.
	Price Value
}

// Trade describes an expected match against an offer or a liquidity pool.
//
// The amounts and assets are from the matched side's point of view, the way
// the protocol records a trade: Sold is what the offer or pool gave up and
// Bought is what it received. For the account that submitted the transaction
// the two are therefore the other way round, which is worth saying because
// getting it backwards is the easiest mistake to make here.
//
// A zero field is not checked.
type Trade struct {
	// Seller is the account whose offer was matched. Nil matches any seller,
	// and a trade against a liquidity pool has none.
	Seller Value
	// Pool identifies a liquidity pool whose reserves were traded against,
	// in the forms AssertLiquidityPoolDeposit accepts. Nil matches any pool,
	// and a trade against an order-book offer has none.
	Pool Value
	// OfferID is the id of the matched offer. Zero matches any id.
	OfferID int64
	// Sold is the asset the matched offer or pool gave up, and Bought the
	// asset it received. Both accept the forms AssertBalanceChanged accepts;
	// nil matches any asset.
	Sold   Value
	Bought Value
	// AmountSold is how much of Sold the matched side gave up, in stroops,
	// and AmountBought how much of Bought it received. Zero skips the check.
	//
	// A transaction meta cannot answer AmountBought: it records the offers
	// that changed, not what they were paid. Assert it against the
	// transaction result, which carries both halves of every match.
	AmountSold   int64
	AmountBought int64
}

// offerState is a sanitised offer entry: the fields an assertion matches on,
// captured while the value is known to be well-formed.
type offerState struct {
	seller   string
	offerID  int64
	selling  string
	buying   string
	amount   int64
	price    xdr.Price
	restored bool
}

// tradeSource names where a set of trades was read from, so a failure can say
// what the source is able to establish.
type tradeSource int

const (
	// tradeSourceResult is a transaction result's claim atoms: the protocol's
	// own record of each match, with both amounts.
	tradeSourceResult tradeSource = iota
	// tradeSourceMeta is reconstruction from the offer entries a meta shows
	// changing.
	tradeSourceMeta
)

func (s tradeSource) String() string {
	if s == tradeSourceResult {
		return "transaction result"
	}
	return "transaction meta"
}

// tradeMatch is one match, from either source.
type tradeMatch struct {
	source tradeSource
	// seller is the matched offer's account, empty for a pool trade.
	seller string
	// pool is the matched liquidity pool id in hex, empty for an order-book
	// trade.
	pool string
	// offerID is the matched offer's id, zero for a pool trade.
	offerID int64
	// sold and bought are canonical asset labels, from the matched side's
	// point of view.
	sold, bought string
	// amountSold is how much of sold the matched side gave up.
	amountSold int64
	// amountBought is how much of bought it received, and hasBought reports
	// whether the source establishes it.
	amountBought int64
	hasBought    bool
	// opIndex is the operation the match belongs to, for a result source, and
	// -1 for a meta source, which does not attribute a change to an
	// operation.
	opIndex int
	// fullyFilled reports that nothing was left of the offer, which for a
	// meta source means its entry was removed.
	fullyFilled bool
}

// AssertOfferCreated checks that the transaction put a matching offer in the
// order book.
//
// An offer that was completely filled on submission creates no entry, so
// there is nothing for this to find; that is a trade, and
// AssertTradeExecuted is the assertion for it. An offer that was partly
// filled does create an entry, holding the remainder, so Amount is the amount
// left rather than the amount submitted.
//
// An offer entry restored from the Soroban archival state counts as created,
// as it does for AssertEntryCreated.
func AssertOfferCreated(t TB, meta Value, want Offer) bool {
	t.Helper()
	const assertion = "AssertOfferCreated"

	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}
	query, ok := resolveOffer(t, assertion, want)
	if !ok {
		return false
	}
	created, ok := createdOffers(t, assertion, changes)
	if !ok {
		return false
	}

	if len(created) == 0 {
		t.Errorf("%s: the transaction created no offer; want %s\n"+
			"An offer that was filled completely on submission never reaches the "+
			"ledger — assert the trade with AssertTradeExecuted instead.\n\n%s",
			assertion, query.describe(), indent(describeOfferChanges(changes), "  "))
		return false
	}
	for _, o := range created {
		if query.matches(o) {
			return true
		}
	}

	// With one offer created the message can name the fields that differ;
	// with several it lists them, since picking one would be arbitrary.
	if len(created) == 1 {
		t.Errorf("%s", offerMismatchMessage(assertion, query, created[0]))
		return false
	}
	t.Errorf("%s: none of the %d offers this transaction created is %s\n\n%s",
		assertion, len(created), query.describe(), indent(describeOffers(created), "  "))
	return false
}

// AssertTradeExecuted checks that the transaction matched an offer or a
// liquidity pool.
//
// value accepts either a transaction result or a transaction meta, and the
// difference matters:
//
//   - A result carries the protocol's own record of each match — a claim atom
//     per fill, with the assets and both amounts — so it can answer every
//     field of Trade. Prefer it.
//   - A meta carries the offer entries that changed, from which the amount an
//     offer gave up can be measured but not what it was paid. It also cannot
//     say why an offer shrank: an owner reducing their own offer looks exactly
//     like one that was matched.
//
// Trades against a liquidity pool only exist in the result; a pool's reserves
// moving is a change to the pool entry, which AssertLiquidityPoolDeposit and
// AssertLiquidityPoolWithdrawal read.
func AssertTradeExecuted(t TB, value Value, want Trade) bool {
	t.Helper()
	const assertion = "AssertTradeExecuted"

	trades, source, ok := tradesFrom(t, assertion, value)
	if !ok {
		return false
	}
	query, ok := resolveTrade(t, assertion, want)
	if !ok {
		return false
	}

	if query.amountBought != 0 && source == tradeSourceMeta {
		t.Errorf("%s: a transaction meta records the offers that changed, not what they "+
			"were paid, so AmountBought cannot be checked against one; "+
			"pass the transaction result, whose claim atoms carry both amounts",
			assertion)
		return false
	}

	if len(trades) == 0 {
		t.Errorf("%s: this %s records no trade; want %s",
			assertion, source, query.describe())
		return false
	}
	for _, tr := range trades {
		if query.matches(tr) {
			return true
		}
	}

	t.Errorf("%s: no trade in this %s is %s\n\n%s",
		assertion, source, query.describe(), indent(describeTrades(trades), "  "))
	return false
}

// offerQuery is a resolved Offer: every field reduced to the canonical form a
// sanitised entry carries, so matching is comparison rather than coercion.
type offerQuery struct {
	seller   string
	offerID  int64
	selling  string
	buying   string
	amount   int64
	price    xdr.Price
	hasPrice bool
}

// matches reports whether an offer satisfies every field the query sets.
func (q offerQuery) matches(o offerState) bool {
	switch {
	case q.seller != "" && q.seller != o.seller:
		return false
	case q.offerID != 0 && q.offerID != o.offerID:
		return false
	case q.selling != "" && q.selling != o.selling:
		return false
	case q.buying != "" && q.buying != o.buying:
		return false
	case q.amount != 0 && q.amount != o.amount:
		return false
	case q.hasPrice && !samePrice(q.price, o.price):
		return false
	}
	return true
}

// describe renders the query as the offer a test asked for.
func (q offerQuery) describe() string {
	parts := make([]string, 0, 6)
	if q.selling != "" || q.buying != "" {
		parts = append(parts, fmt.Sprintf("selling %s for %s",
			orAny(q.selling), orAny(q.buying)))
	}
	if q.amount != 0 {
		parts = append(parts, "amount "+amountStroops(xdr.Int64(q.amount), ""))
	}
	if q.hasPrice {
		parts = append(parts, "price "+describePrice(q.price))
	}
	if q.seller != "" {
		parts = append(parts, "from account "+shorten(q.seller))
	}
	if q.offerID != 0 {
		parts = append(parts, "id "+strconv.FormatInt(q.offerID, 10))
	}
	if len(parts) == 0 {
		return "an offer"
	}
	return "an offer " + strings.Join(parts, ", ")
}

// resolveOffer coerces every field of an Offer and reports a test failure
// when one cannot be read.
func resolveOffer(t TB, assertion string, want Offer) (offerQuery, bool) {
	t.Helper()

	var q offerQuery
	if want.Seller != nil {
		addr, ok := resolveAccount(t, assertion, "Seller", want.Seller)
		if !ok {
			return offerQuery{}, false
		}
		q.seller = addr
	}
	if want.Selling != nil {
		spec, ok := resolveAsset(t, assertion, "Selling", want.Selling)
		if !ok {
			return offerQuery{}, false
		}
		q.selling = spec.id
	}
	if want.Buying != nil {
		spec, ok := resolveAsset(t, assertion, "Buying", want.Buying)
		if !ok {
			return offerQuery{}, false
		}
		q.buying = spec.id
	}
	if want.Price != nil {
		p, ok := resolvePrice(t, assertion, want.Price)
		if !ok {
			return offerQuery{}, false
		}
		q.price, q.hasPrice = p, true
	}
	q.offerID, q.amount = want.OfferID, want.Amount
	return q, true
}

// tradeQuery is a resolved Trade.
type tradeQuery struct {
	seller       string
	pool         string
	offerID      int64
	sold, bought string
	amountSold   int64
	amountBought int64
}

// matches reports whether a trade satisfies every field the query sets.
func (q tradeQuery) matches(tr tradeMatch) bool {
	switch {
	case q.seller != "" && q.seller != tr.seller:
		return false
	case q.pool != "" && q.pool != tr.pool:
		return false
	case q.offerID != 0 && q.offerID != tr.offerID:
		return false
	case q.sold != "" && q.sold != tr.sold:
		return false
	case q.bought != "" && q.bought != tr.bought:
		return false
	case q.amountSold != 0 && q.amountSold != tr.amountSold:
		return false
	case q.amountBought != 0 && (!tr.hasBought || q.amountBought != tr.amountBought):
		return false
	}
	return true
}

// describe renders the query as the trade a test asked for.
func (q tradeQuery) describe() string {
	parts := make([]string, 0, 6)
	if q.sold != "" || q.bought != "" {
		parts = append(parts, fmt.Sprintf("selling %s for %s", orAny(q.sold), orAny(q.bought)))
	}
	if q.amountSold != 0 {
		parts = append(parts, "amount sold "+amountStroops(xdr.Int64(q.amountSold), ""))
	}
	if q.amountBought != 0 {
		parts = append(parts, "amount bought "+amountStroops(xdr.Int64(q.amountBought), ""))
	}
	if q.seller != "" {
		parts = append(parts, "against account "+shorten(q.seller))
	}
	if q.pool != "" {
		parts = append(parts, "against pool "+shortenID(q.pool))
	}
	if q.offerID != 0 {
		parts = append(parts, "offer id "+strconv.FormatInt(q.offerID, 10))
	}
	if len(parts) == 0 {
		return "a trade"
	}
	return "a trade " + strings.Join(parts, ", ")
}

// resolveTrade coerces every field of a Trade and reports a test failure when
// one cannot be read.
func resolveTrade(t TB, assertion string, want Trade) (tradeQuery, bool) {
	t.Helper()

	var q tradeQuery
	if want.Seller != nil {
		addr, ok := resolveAccount(t, assertion, "Seller", want.Seller)
		if !ok {
			return tradeQuery{}, false
		}
		q.seller = addr
	}
	if want.Pool != nil {
		id, ok := resolvePoolID(t, assertion, want.Pool)
		if !ok {
			return tradeQuery{}, false
		}
		q.pool = id
	}
	if want.Sold != nil {
		spec, ok := resolveAsset(t, assertion, "Sold", want.Sold)
		if !ok {
			return tradeQuery{}, false
		}
		q.sold = spec.id
	}
	if want.Bought != nil {
		spec, ok := resolveAsset(t, assertion, "Bought", want.Bought)
		if !ok {
			return tradeQuery{}, false
		}
		q.bought = spec.id
	}
	q.offerID, q.amountSold, q.amountBought = want.OfferID, want.AmountSold, want.AmountBought
	return q, true
}

// createdOffers collects the offers a transaction put in the order book, in
// the order the protocol applied them.
func createdOffers(t TB, assertion string, changes []ledgerChange) ([]offerState, bool) {
	var out []offerState
	ok := guard(t, assertion, func() {
		for _, c := range changes {
			if c.entryType != xdr.LedgerEntryTypeOffer {
				continue
			}
			if c.kind != xdr.LedgerEntryChangeTypeLedgerEntryCreated &&
				c.kind != xdr.LedgerEntryChangeTypeLedgerEntryRestored {
				continue
			}
			o, has := offerStateOf(c.entry)
			if !has {
				continue
			}
			o.restored = c.kind == xdr.LedgerEntryChangeTypeLedgerEntryRestored
			out = append(out, o)
		}
	})
	if !ok {
		return nil, false
	}
	return out, true
}

// offerStateOf reads a change's offer entry. It calls generated accessors, so
// it must run behind guard.
func offerStateOf(entry *xdr.LedgerEntry) (offerState, bool) {
	if entry == nil || entry.Data.Type != xdr.LedgerEntryTypeOffer || entry.Data.Offer == nil {
		return offerState{}, false
	}
	o := entry.Data.Offer
	return offerState{
		seller:  o.SellerId.Address(),
		offerID: int64(o.OfferId),
		selling: o.Selling.StringCanonical(),
		buying:  o.Buying.StringCanonical(),
		amount:  int64(o.Amount),
		price:   o.Price,
	}, true
}

// tradesFrom reads every trade a value records, and says which kind of value
// it was read from.
func tradesFrom(t TB, assertion string, v Value) ([]tradeMatch, tradeSource, bool) {
	t.Helper()

	switch val := v.(type) {
	case xdr.TransactionResult:
		return claimedTrades(t, assertion, &val)
	case *xdr.TransactionResult:
		if val == nil {
			t.Errorf("%s: value could not be read: value is a nil *xdr.TransactionResult", assertion)
			return nil, tradeSourceResult, false
		}
		return claimedTrades(t, assertion, val)
	case xdr.TransactionMeta:
		return metaTrades(t, assertion, &val)
	case *xdr.TransactionMeta:
		if val == nil {
			t.Errorf("%s: value could not be read: value is a nil *xdr.TransactionMeta", assertion)
			return nil, tradeSourceMeta, false
		}
		return metaTrades(t, assertion, val)
	}

	decoded, ok := resolve(t, assertion, "", v, "")
	if !ok {
		return nil, tradeSourceResult, false
	}
	switch raw := decoded.Raw.(type) {
	case *xdr.TransactionResult:
		return claimedTrades(t, assertion, raw)
	case *xdr.TransactionMeta:
		return metaTrades(t, assertion, raw)
	default:
		t.Errorf("%s: value is a %s, want a TransactionResult or a TransactionMeta; "+
			"a trade is recorded in a result's claim atoms, and visible in a meta as the "+
			"offers that changed", assertion, decoded.Type)
		return nil, tradeSourceResult, false
	}
}

// claimedTrades reads the claim atoms of every operation in a result.
func claimedTrades(t TB, assertion string, res *xdr.TransactionResult) ([]tradeMatch, tradeSource, bool) {
	t.Helper()

	var out []tradeMatch
	ok := guard(t, assertion, func() {
		if res.Result.Results == nil {
			return
		}
		for i, opResult := range *res.Result.Results {
			for _, atom := range claimAtomsOf(opResult) {
				tr, has := tradeFromAtom(atom)
				if !has {
					continue
				}
				tr.opIndex = i
				out = append(out, tr)
			}
		}
	})
	if !ok {
		return nil, tradeSourceResult, false
	}
	return out, tradeSourceResult, true
}

// claimAtomsOf returns the claim atoms one operation result carries. The four
// operations that can match against the order book are the two manage-offer
// forms, the passive variant, and the two path payments.
func claimAtomsOf(opResult xdr.OperationResult) []xdr.ClaimAtom {
	if opResult.Code != xdr.OperationResultCodeOpInner || opResult.Tr == nil {
		return nil
	}
	tr := opResult.Tr

	switch tr.Type {
	case xdr.OperationTypeManageSellOffer:
		if r := tr.ManageSellOfferResult; r != nil && r.Success != nil {
			return r.Success.OffersClaimed
		}
	case xdr.OperationTypeCreatePassiveSellOffer:
		if r := tr.CreatePassiveSellOfferResult; r != nil && r.Success != nil {
			return r.Success.OffersClaimed
		}
	case xdr.OperationTypeManageBuyOffer:
		if r := tr.ManageBuyOfferResult; r != nil && r.Success != nil {
			return r.Success.OffersClaimed
		}
	case xdr.OperationTypePathPaymentStrictReceive:
		if r := tr.PathPaymentStrictReceiveResult; r != nil && r.Success != nil {
			return r.Success.Offers
		}
	case xdr.OperationTypePathPaymentStrictSend:
		if r := tr.PathPaymentStrictSendResult; r != nil && r.Success != nil {
			return r.Success.Offers
		}
	}
	return nil
}

// tradeFromAtom converts one claim atom. It calls generated accessors, so it
// must run behind guard.
func tradeFromAtom(atom xdr.ClaimAtom) (tradeMatch, bool) {
	out := tradeMatch{source: tradeSourceResult, hasBought: true, opIndex: -1}

	switch atom.Type {
	case xdr.ClaimAtomTypeClaimAtomTypeV0:
		a := atom.V0
		if a == nil {
			return tradeMatch{}, false
		}
		// The pre-protocol-11 arm identifies the seller by raw key rather
		// than by account id, so rebuild the address from it.
		var aid xdr.AccountId
		key := a.SellerEd25519
		aid.Type = xdr.PublicKeyTypePublicKeyTypeEd25519
		aid.Ed25519 = &key
		out.seller = aid.Address()
		out.offerID = int64(a.OfferId)
		out.sold, out.bought = a.AssetSold.StringCanonical(), a.AssetBought.StringCanonical()
		out.amountSold, out.amountBought = int64(a.AmountSold), int64(a.AmountBought)
	case xdr.ClaimAtomTypeClaimAtomTypeOrderBook:
		a := atom.OrderBook
		if a == nil {
			return tradeMatch{}, false
		}
		out.seller = a.SellerId.Address()
		out.offerID = int64(a.OfferId)
		out.sold, out.bought = a.AssetSold.StringCanonical(), a.AssetBought.StringCanonical()
		out.amountSold, out.amountBought = int64(a.AmountSold), int64(a.AmountBought)
	case xdr.ClaimAtomTypeClaimAtomTypeLiquidityPool:
		a := atom.LiquidityPool
		if a == nil {
			return tradeMatch{}, false
		}
		out.pool = describePoolID(a.LiquidityPoolId)
		out.sold, out.bought = a.AssetSold.StringCanonical(), a.AssetBought.StringCanonical()
		out.amountSold, out.amountBought = int64(a.AmountSold), int64(a.AmountBought)
	default:
		return tradeMatch{}, false
	}
	return out, true
}

// metaTrades reconstructs the matches a meta shows: every offer that existed
// before the transaction and had less of its asset afterwards.
//
// The amount an offer gave up is exact — it is the difference between the
// entry's state and its last recorded value — but what it received is not
// recorded anywhere in the meta, so it is left unestablished rather than
// derived from the price, whose rounding is the protocol's business.
func metaTrades(t TB, assertion string, m *xdr.TransactionMeta) ([]tradeMatch, tradeSource, bool) {
	t.Helper()

	var changes []ledgerChange
	if !guard(t, assertion, func() { changes = sanitiseChanges(metaChanges(m)) }) {
		return nil, tradeSourceMeta, false
	}

	type movement struct {
		before    offerState
		after     offerState
		hasBefore bool
		removed   bool
	}
	var (
		order []string
		byKey = make(map[string]*movement)
	)
	ok := guard(t, assertion, func() {
		for _, c := range changes {
			if c.entryType != xdr.LedgerEntryTypeOffer {
				continue
			}
			mv, seen := byKey[c.keyID]
			if !seen {
				mv = &movement{}
				byKey[c.keyID] = mv
				order = append(order, c.keyID)
			}

			o, has := offerStateOf(c.entry)
			switch c.kind {
			case xdr.LedgerEntryChangeTypeLedgerEntryState:
				if has && !mv.hasBefore {
					mv.before, mv.hasBefore = o, true
				}
				if has {
					mv.after = o
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
				xdr.LedgerEntryChangeTypeLedgerEntryRestored,
				xdr.LedgerEntryChangeTypeLedgerEntryCreated:
				if has {
					mv.after = o
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
				mv.removed = true
			}
		}
	})
	if !ok {
		return nil, tradeSourceMeta, false
	}

	out := make([]tradeMatch, 0, len(order))
	for _, k := range order {
		mv := byKey[k]
		// An offer with no recorded prior state is one this transaction
		// created, so it gave nothing up.
		if !mv.hasBefore {
			continue
		}
		remaining := mv.after.amount
		if mv.removed {
			remaining = 0
		}
		sold := mv.before.amount - remaining
		if sold <= 0 {
			continue
		}
		out = append(out, tradeMatch{
			source:      tradeSourceMeta,
			seller:      mv.before.seller,
			offerID:     mv.before.offerID,
			sold:        mv.before.selling,
			bought:      mv.before.buying,
			amountSold:  sold,
			opIndex:     -1,
			fullyFilled: mv.removed,
		})
	}
	return out, tradeSourceMeta, true
}

// resolvePrice coerces a price argument and reports a test failure when it
// cannot.
func resolvePrice(t TB, assertion string, v Value) (xdr.Price, bool) {
	t.Helper()

	var (
		p   xdr.Price
		err error
	)
	if !guard(t, assertion, func() { p, err = priceOf(v) }) {
		return xdr.Price{}, false
	}
	if err != nil {
		t.Errorf("%s: Price could not be read: %v", assertion, err)
		return xdr.Price{}, false
	}
	return p, true
}

// priceOf accepts a price in the forms callers hold it in.
func priceOf(v Value) (xdr.Price, error) {
	switch val := v.(type) {
	case string:
		return priceFromString(val)
	case xdr.Price:
		return validPrice(val)
	case *xdr.Price:
		if val == nil {
			return xdr.Price{}, fmt.Errorf("value is a nil *xdr.Price")
		}
		return validPrice(*val)
	case *lens.Value:
		if val == nil {
			return xdr.Price{}, fmt.Errorf("value is a nil *lens.Value")
		}
		p, ok := val.Raw.(*xdr.Price)
		if !ok {
			return xdr.Price{}, fmt.Errorf("value is a %s, want a Price", val.Type)
		}
		return validPrice(*p)
	default:
		return xdr.Price{}, fmt.Errorf(
			"unsupported type %T; pass a price as \"N/D\" or an xdr.Price", v)
	}
}

// priceFromString parses the "N/D" form, the way a price reads in a failure
// message and in the XDR.
func priceFromString(s string) (xdr.Price, error) {
	trimmed := strings.TrimSpace(s)
	n, d, found := strings.Cut(trimmed, "/")
	if !found {
		return xdr.Price{}, fmt.Errorf(
			"%q is not a price; a price is a ratio written \"N/D\", such as \"1/2\"", s)
	}
	num, err := strconv.ParseInt(strings.TrimSpace(n), 10, 32)
	if err != nil {
		return xdr.Price{}, fmt.Errorf("invalid numerator in %q: %w", s, err)
	}
	den, err := strconv.ParseInt(strings.TrimSpace(d), 10, 32)
	if err != nil {
		return xdr.Price{}, fmt.Errorf("invalid denominator in %q: %w", s, err)
	}
	return validPrice(xdr.Price{N: xdr.Int32(num), D: xdr.Int32(den)})
}

// validPrice rejects the prices that cannot be compared as a ratio.
func validPrice(p xdr.Price) (xdr.Price, error) {
	if p.D == 0 {
		return xdr.Price{}, fmt.Errorf("a price cannot have a denominator of zero")
	}
	if p.N < 0 || p.D < 0 {
		return xdr.Price{}, fmt.Errorf("a price cannot be negative, got %s", describePrice(p))
	}
	return p, nil
}

// samePrice compares two prices as ratios, so an unreduced fraction matches
// the reduced one it is equal to.
func samePrice(a, b xdr.Price) bool {
	return int64(a.N)*int64(b.D) == int64(b.N)*int64(a.D)
}

// offerMismatchMessage names the fields of a single created offer that do not
// match what was wanted.
func offerMismatchMessage(assertion string, q offerQuery, got offerState) string {
	diffs := make([]string, 0, 6)
	if q.seller != "" && q.seller != got.seller {
		diffs = append(diffs, fmt.Sprintf("~ seller is %s, want %s",
			shorten(got.seller), shorten(q.seller)))
	}
	if q.offerID != 0 && q.offerID != got.offerID {
		diffs = append(diffs, fmt.Sprintf("~ offer id is %d, want %d", got.offerID, q.offerID))
	}
	if q.selling != "" && q.selling != got.selling {
		diffs = append(diffs, fmt.Sprintf("~ selling %s, want %s", got.selling, q.selling))
	}
	if q.buying != "" && q.buying != got.buying {
		diffs = append(diffs, fmt.Sprintf("~ buying %s, want %s", got.buying, q.buying))
	}
	if q.amount != 0 && q.amount != got.amount {
		diffs = append(diffs, fmt.Sprintf("~ amount is %s, want %s",
			amountStroops(xdr.Int64(got.amount), ""), amountStroops(xdr.Int64(q.amount), "")))
	}
	if q.hasPrice && !samePrice(q.price, got.price) {
		diffs = append(diffs, fmt.Sprintf("~ price is %s, want %s",
			describePrice(got.price), describePrice(q.price)))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: the offer this transaction created is not %s — %s\n\n",
		assertion, q.describe(), pluralChanges(len(diffs)))
	for _, d := range diffs {
		fmt.Fprintf(&b, "  %s\n", d)
	}
	b.WriteString("\n")
	b.WriteString(indent(describeOffers([]offerState{got}), "  "))
	return b.String()
}

// describeOffers lists the offers created, so a failed lookup shows what the
// transaction did put on the book.
func describeOffers(offers []offerState) string {
	if len(offers) == 0 {
		return "the transaction created no offer"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "offers created (%d):\n", len(offers))
	for i, o := range offers {
		verb := "created"
		if o.restored {
			verb = "restored"
		}
		fmt.Fprintf(&b, "  [%d] %s offer #%d by %s: selling %s for %s, amount %s, price %s\n",
			i, verb, o.offerID, shorten(o.seller), o.selling, o.buying,
			amountStroops(xdr.Int64(o.amount), ""), describePrice(o.price))
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeTrades lists the trades found, naming for each what its source
// establishes.
func describeTrades(trades []tradeMatch) string {
	if len(trades) == 0 {
		return "no trade was recorded"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "trades recorded (%d):\n", len(trades))
	for i, tr := range trades {
		against := "account " + shorten(tr.seller)
		if tr.pool != "" {
			against = "pool " + shortenID(tr.pool)
		}
		fmt.Fprintf(&b, "  [%d] %s", i, against)
		if tr.offerID != 0 {
			fmt.Fprintf(&b, " offer #%d", tr.offerID)
		}
		if tr.opIndex >= 0 {
			fmt.Fprintf(&b, " (operation %d)", tr.opIndex)
		}
		fmt.Fprintf(&b, " sold %s %s", amountStroops(xdr.Int64(tr.amountSold), ""), tr.sold)
		if tr.hasBought {
			fmt.Fprintf(&b, " for %s %s", amountStroops(xdr.Int64(tr.amountBought), ""), tr.bought)
		} else {
			fmt.Fprintf(&b, " for %s (amount not recorded in a meta)", tr.bought)
		}
		if tr.fullyFilled {
			b.WriteString(", offer fully filled")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeOfferChanges lists only the changes touching offers, so a failed
// lookup names what happened on the book rather than in the whole ledger.
func describeOfferChanges(changes []ledgerChange) string {
	matching := make([]ledgerChange, 0, len(changes))
	for _, c := range changes {
		if c.entryType == xdr.LedgerEntryTypeOffer {
			matching = append(matching, c)
		}
	}
	if len(matching) == 0 {
		return "no ledger entry change in this meta touches an offer"
	}
	return describeChanges(matching)
}

// describePrice renders a price as the ratio the protocol stores.
func describePrice(p xdr.Price) string {
	return fmt.Sprintf("%d/%d", int32(p.N), int32(p.D))
}

// orAny names an unset part of a query, so a wanted description reads as a
// sentence whichever fields were given.
func orAny(label string) string {
	if label == "" {
		return "any asset"
	}
	return label
}
