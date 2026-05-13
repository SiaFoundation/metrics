package metrics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/chain"
	"go.sia.tech/coreutils/threadgroup"
	"go.uber.org/zap"
)

// ContractResolutionType represents the type of contract resolution.
const (
	ResolutionTypeProof = iota + 1
	ResolutionTypeExpired
	ResolutionTypeRenewed
	// ResolutionTypeAbandoned is a metric-only resolution emitted when a
	// previously-tracked v2 file contract transitions to a malformed state
	// (one the metric model can't follow — see wellFormedV2Contract). The
	// contract may still resolve on chain at some future point, but the
	// indexer can't safely apply further events to it, so it is removed
	// from active counters here. No success/failure/renewed classification
	// is attributed; HostBurn and HostEarnedRevenue remain zero.
	ResolutionTypeAbandoned
)

type (
	// A ContractResolutionType represents the type of contract resolution.
	ContractResolutionType uint8

	// A Host represents the metrics for a host.
	Host struct {
		PublicKey           types.PublicKey `json:"publicKey"`
		ActiveContracts     uint64          `json:"activeContracts"`
		RenewedContracts    uint64          `json:"renewedContracts"`
		SuccessfulContracts uint64          `json:"successfulContracts"`
		FailedContracts     uint64          `json:"failedContracts"`
		// RevisionCount is the cumulative number of v2 contract revision
		// events for this host across all indexed blocks. One block can
		// contain at most one revision per contract.
		RevisionCount uint64 `json:"revisionCount"`

		ActiveSize uint64 `json:"activeSize"`
		TotalSize  uint64 `json:"totalSize"`
		// BytesUploaded is the cumulative, monotonically-increasing total
		// of bytes added to this host's contracts. Unlike TotalSize (which
		// tracks chain-state size and decreases when contracts shrink),
		// BytesUploaded only ever increases — on each formation by the
		// contract's initial Size, and on each revision by max(0, NewSize - ExistingSize).
		BytesUploaded uint64 `json:"bytesUploaded"`

		LockedCollateral types.Currency `json:"lockedCollateral"`
		RiskedCollateral types.Currency `json:"riskedCollateral"`
		BurntCollateral  types.Currency `json:"burntCollateral"`
		PotentialRevenue types.Currency `json:"potentialRevenue"`
		EarnedRevenue    types.Currency `json:"earnedRevenue"`

		// FirstSeen is the truncated-hour timestamp of the first block in
		// which this host had any v2 contract event (formation, revision,
		// or resolution). Used to derive "new hosts in period" metrics.
		FirstSeen time.Time `json:"firstSeen"`
		// LastActive is the truncated-hour timestamp of the most recent
		// block in which this host had any v2 contract event.
		LastActive time.Time `json:"lastActive"`
		Timestamp  time.Time `json:"timestamp"`
	}

	// A Renter represents the metrics for a renter.
	Renter struct {
		PublicKey types.PublicKey `json:"publicKey"`

		ActiveContracts     uint64 `json:"activeContracts"`
		RenewedContracts    uint64 `json:"renewedContracts"`
		SuccessfulContracts uint64 `json:"successfulContracts"`
		FailedContracts     uint64 `json:"failedContracts"`
		// RevisionCount is the cumulative number of v2 contract revision
		// events for this renter across all indexed blocks.
		RevisionCount uint64 `json:"revisionCount"`

		ActiveSize uint64 `json:"activeSize"`
		TotalSize  uint64 `json:"totalSize"`
		// BytesUploaded is the cumulative, monotonically-increasing total
		// of bytes this renter has added to their contracts. See Host.BytesUploaded
		// for the same semantics applied to the host side.
		BytesUploaded uint64 `json:"bytesUploaded"`

		// Spent is the cumulative SC the renter has paid to hosts via
		// contracts: the upfront contract price at each formation, the
		// allowance consumed by on-chain storage revisions, and the
		// off-chain accrual surfaced at each renewal resolution (see
		// ContractResolution.RenterDeferredSpend). Does not include Tax
		// or miner fees.
		Spent  types.Currency `json:"spent"`
		Locked types.Currency `json:"locked"`
		// Tax is the cumulative siafund tax the renter has paid on contract
		// formations and renewals.
		Tax types.Currency `json:"tax"`

		// FirstSeen is the truncated-hour timestamp of the first block in
		// which this renter had any v2 contract event. Used to derive
		// "new renters in period" metrics.
		FirstSeen time.Time `json:"firstSeen"`
		// LastActive is the truncated-hour timestamp of the most recent
		// block in which this renter had any v2 contract event.
		LastActive time.Time `json:"lastActive"`
		Timestamp  time.Time `json:"timestamp"`
	}

	// Metrics represents the global metrics.
	Metrics struct {
		Renters uint64 `json:"renters"`
		Hosts   uint64 `json:"hosts"`

		ActiveContracts     uint64 `json:"activeContracts"`
		RenewedContracts    uint64 `json:"renewedContracts"`
		SuccessfulContracts uint64 `json:"successfulContracts"`
		FailedContracts     uint64 `json:"failedContracts"`
		// TransactionCount is the cumulative number of v2 transactions
		// in all indexed blocks. Daily on-chain activity can be derived
		// by diffing two snapshots a day apart.
		TransactionCount uint64 `json:"transactionCount"`
		// RevisionCount is the cumulative number of v2 contract revision
		// events across all indexed blocks. One block can contain at
		// most one revision per contract.
		RevisionCount uint64 `json:"revisionCount"`

		ActiveSize uint64 `json:"activeSize"`
		TotalSize  uint64 `json:"totalSize"`
		// BytesUploaded is the cumulative, monotonically-increasing total
		// of bytes ever added to the network across all contracts. Unlike
		// TotalSize (which decreases on shrink revisions), this only ever
		// grows — on each formation by Size, and on each revision by
		// max(0, NewSize - ExistingSize).
		BytesUploaded uint64 `json:"bytesUploaded"`
		// ActiveByteDays is the cumulative byte-days of storage held by
		// the network — for each indexed block, ActiveSize * block_interval
		// (in days) is added. This captures the protocol's "work done" unit
		// (the host's per-byte storage payout).
		ActiveByteDays uint64 `json:"activeByteDays"`

		SpentAllowance  types.Currency `json:"spentAllowance"`
		LockedAllowance types.Currency `json:"lockedAllowance"`
		// Tax is the cumulative siafund tax paid across all v2 contract
		// formations and renewals. Tax is paid to siafund holders and is
		// not part of SpentAllowance (which only counts SC flowing to hosts).
		Tax types.Currency `json:"tax"`

		PotentialRevenue types.Currency `json:"potentialRevenue"`
		EarnedRevenue    types.Currency `json:"earnedRevenue"`
		LockedCollateral types.Currency `json:"lockedCollateral"`
		RiskedCollateral types.Currency `json:"riskedCollateral"`
		BurntCollateral  types.Currency `json:"burntCollateral"`

		Timestamp time.Time `json:"timestamp"`
	}

	// A ContractFormation represents the formation of a new contract.
	ContractFormation struct {
		Host   types.PublicKey
		Renter types.PublicKey
		Size   uint64
		// BytesUploaded is the amount this formation event contributes to
		// the host's, renter's, and global BytesUploaded counters. For a
		// fresh formation this is the contract's initial Filesize. For a
		// renewal-created formation it is max(0, newFC.Filesize −
		// parent.Filesize) — i.e., only the growth that the renewal makes
		// visible (off-chain uploads on the parent that surface here). The
		// parent's prior Filesize was already credited when the parent was
		// formed (or grew via on-chain revision); double-counting it here
		// would inflate BytesUploaded with every renewal.
		BytesUploaded uint64

		// RenterAllowance is the refundable allowance locked into the
		// contract at formation (= RenterOutput.Value).
		RenterAllowance types.Currency
		// RenterContractPrice is the SC the renter has irrevocably paid
		// to the host at formation (= HostOutput.Value - TotalCollateral).
		// For fresh formations this is just the contract price; for
		// contracts created by a renewal it also includes any pre-paid
		// storage cost rolled into HostOutput.Value.
		RenterContractPrice types.Currency
		// RenterTax is the siafund tax levied on this contract at
		// formation. It is paid by the renter to siafund holders and is
		// not refundable.
		RenterTax types.Currency

		HostLockedCollateral types.Currency
		HostRiskedCollateral types.Currency
		HostPotentialRevenue types.Currency
	}

	// A ContractRevision represents a revision of an existing contract.
	ContractRevision struct {
		Host   types.PublicKey
		Renter types.PublicKey

		ExistingSize uint64
		NewSize      uint64

		ExistingAllowance types.Currency
		NewAllowance      types.Currency

		ExistingRiskedCollateral types.Currency
		NewRiskedCollateral      types.Currency

		ExistingPotentialRevenue types.Currency
		NewPotentialRevenue      types.Currency
	}

	// A ContractResolution represents the resolution of a contract.
	ContractResolution struct {
		Host   types.PublicKey
		Renter types.PublicKey
		Size   uint64
		Type   ContractResolutionType

		// RenterAllowance is the refundable allowance still locked in
		// the contract at the time of resolution (= RenterOutput.Value).
		// On any resolution path it stops being locked; on a renewal
		// the RenterRollover portion is re-locked into the new contract.
		RenterAllowance types.Currency
		// RenterDeferredSpend is the renter's off-chain spending on this
		// contract that the metric model couldn't observe during its life,
		// surfaced at a renewal resolution. It is the gap between the latest
		// off-chain RiskedHostRevenue (reconstructed from the resolution as
		// FinalHostOutput.Value + HostRollover − parent.TotalCollateral) and
		// the on-chain parent.RiskedHostRevenue() — i.e., the SC the renter
		// paid into host revenue via off-chain revisions between the parent's
		// last on-chain state and the renewal. Only set on renewal-type
		// resolutions; zero otherwise. Credited to rm.Spent and
		// gm.SpentAllowance at apply time so renter cumulative spending
		// telescopes through renewal lineages instead of falling behind host
		// EarnedRevenue.
		RenterDeferredSpend types.Currency

		HostLockedCollateral types.Currency
		HostRiskedCollateral types.Currency
		HostBurn             types.Currency
		HostPotentialRevenue types.Currency
		HostEarnedRevenue    types.Currency
	}

	// A State represents the state of the chain at a specific timestamp.
	State struct {
		Timestamp time.Time `json:"timestamp"`

		// V2TransactionCount is the total number of v2 transactions in
		// the block this state was parsed from. It is added to the
		// global TransactionCount on apply (subtracted on revert).
		V2TransactionCount uint64 `json:"v2TransactionCount"`
		// BlockInterval is the network's consensus block interval at the
		// block this state was parsed from. Used to compute byte-days
		// contributions for ActiveByteDays.
		BlockInterval time.Duration `json:"blockInterval"`

		Formations  []ContractFormation  `json:"formations"`
		Revisions   []ContractRevision   `json:"revisions"`
		Resolutions []ContractResolution `json:"validResolutions"`
	}

	// A Chain provides access to the chain state and updates.
	Chain interface {
		TipState() consensus.State
		UpdatesSince(index types.ChainIndex, limit int) ([]chain.RevertUpdate, []chain.ApplyUpdate, error)
		BestIndex(uint64) (types.ChainIndex, bool)
		OnReorg(func(types.ChainIndex)) func()
		PruneBlocks(height uint64)
	}

	// A Store provides access to the persistent store for metrics.
	Store interface {
		LastIndexedTip(context.Context) (types.ChainIndex, error)
		RevertState(context.Context, types.ChainIndex, State) error
		ApplyState(context.Context, types.ChainIndex, State) error

		RenterMetric(context.Context, types.PublicKey, time.Time) (Renter, error)
		HostMetric(context.Context, types.PublicKey, time.Time) (Host, error)
		GlobalMetric(context.Context, time.Time) (Metrics, error)

		TopHosts(ctx context.Context, start, end time.Time, limit int) ([]Host, error)
		TopRenters(ctx context.Context, start, end time.Time, limit int) ([]Renter, error)
		// TopHostsBySize / TopRentersBySize rank by the latest active_size
		// snapshot in [start, end] rather than by revenue / spending.
		TopHostsBySize(ctx context.Context, start, end time.Time, limit int) ([]Host, error)
		TopRentersBySize(ctx context.Context, start, end time.Time, limit int) ([]Renter, error)

		RenterMetrics(ctx context.Context, renterKey types.PublicKey, start, end time.Time) ([]Renter, error)
		HostMetrics(ctx context.Context, hostKey types.PublicKey, start, end time.Time) ([]Host, error)
		GlobalMetrics(ctx context.Context, start, end time.Time) ([]Metrics, error)

		HostsCount(ctx context.Context, start, end time.Time) (int64, error)
		RentersCount(ctx context.Context, start, end time.Time) (int64, error)

		// NewHosts returns the count of distinct host keys whose first
		// recorded event (FirstSeen) falls in [start, end].
		NewHosts(ctx context.Context, start, end time.Time) (int64, error)
		// NewRenters returns the count of distinct renter keys whose
		// first recorded event (FirstSeen) falls in [start, end].
		NewRenters(ctx context.Context, start, end time.Time) (int64, error)
	}

	// A Manager provides access to the metrics and manages the indexing of the chain state.
	Manager struct {
		tg                   *threadgroup.ThreadGroup
		chain                Chain
		store                Store
		log                  *zap.Logger
		pruneRetentionBlocks uint64
	}

	// ManagerOption configures the metrics Manager.
	ManagerOption func(*Manager)
)

var (
	// ErrTipNotFound is returned when the last indexed tip is not found in the store.
	ErrTipNotFound = fmt.Errorf("tip not found")
	// ErrNotFound is returned when a requested metric is not found.
	ErrNotFound = fmt.Errorf("not found")
)

func subOrZero(a, b types.Currency) types.Currency {
	v, underflow := a.SubWithUnderflow(b)
	if underflow {
		return types.ZeroCurrency
	}
	return v
}

func minCurrency(a, b types.Currency) types.Currency {
	if a.Cmp(b) < 0 {
		return a
	}
	return b
}

func renewalCarriesRevenue(parent, renewal types.V2FileContract) bool {
	return parent.ProofHeight == renewal.ProofHeight &&
		parent.ExpirationHeight == renewal.ExpirationHeight
}

// renewalRevenue partitions the parent contract's latest off-chain
// RiskedHostRevenue between settlement at this resolution (earnedRevenue) and
// roll-forward into the new contract (the implicit difference, equal to
// latest − earned). Returns:
//
//   - newRevenue:    the portion of the new contract's RiskedHostRevenue that
//     is new (not rolled from the parent). This is the renter's
//     new ContractPrice for the renewed contract.
//   - earnedRevenue: the portion of the parent's latest RiskedHostRevenue that
//     the host realizes at this resolution.
//
// The on-chain parent's HostOutput.Value, RenterOutput.Value, and
// MissedHostValue may be stale relative to the latest signed off-chain
// revision — v2 revisions are off-chain by default and only TotalCollateral,
// ProofHeight, ExpirationHeight, and the public keys are consensus-immutable
// across revisions. The latest off-chain HostOutput.Value is recoverable from
// the resolution as FinalHostOutput.Value + HostRollover, per the rhp4
// per-side conservation convention (core/rhp/v4: RenewContract,
// RefreshContractPartialRollover, RefreshContractFullRollover all preserve
// per-side balance even though consensus only enforces the four-way sum).
func renewalRevenue(parent types.V2FileContract, renewal *types.V2FileContractRenewal) (newRevenue, earnedRevenue types.Currency) {
	// subOrZero guards against non-standard renewals on a parent (or with a
	// new contract) where TC > HostOutput.Value — consensus permits this on
	// revisions but rhp4 doesn't produce it. Clamping keeps every contract
	// tracked through its lifecycle.
	finalHostValue := renewal.FinalHostOutput.Value.Add(renewal.HostRollover)
	finalPotentialRevenue := subOrZero(finalHostValue, parent.TotalCollateral)
	newPotentialRevenue := subOrZero(renewal.NewContract.HostOutput.Value, renewal.NewContract.TotalCollateral)

	// Duration-extending renewal (RenewContract): HostRollover = min(parent.TC,
	// new.TC) ≤ parent.TC, so no revenue rolls forward — the full latest
	// RiskedHostRevenue settles at this resolution.
	if !renewalCarriesRevenue(parent, renewal.NewContract) {
		return newPotentialRevenue, finalPotentialRevenue
	}

	// Refresh (RefreshContractFullRollover or RefreshContractPartialRollover):
	// the entire latest RiskedHostRevenue rolls into the new contract; nothing
	// settles as earned at this resolution. In partial-rollover refreshes where
	// rp.Collateral < parent.MissedHostValue, FinalHostOutput.Value > 0
	// represents released unrisked collateral, not earned revenue.
	rolledRevenue := minCurrency(finalPotentialRevenue, newPotentialRevenue)
	return newPotentialRevenue.Sub(rolledRevenue), finalPotentialRevenue.Sub(rolledRevenue)
}

// parseDiffs walks a block's v2 transactions and emits the metric events they
// produce. In v2 every contract event — formation, revision, resolution
// (renewal, storage proof, expiration) — is submitted via a transaction;
// consensus does not synthesize any of them. Iterating transactions directly
// (rather than per-contract chain diffs) lets the renewal branch emit both
// the parent's resolution and the new contract's formation from one call
// site, removing the need to cross-reference a separate "Created" event.
func parseDiffs(timestamp time.Time, cs consensus.State, txns []types.V2Transaction, log *zap.Logger) (State, error) {
	state := State{
		Timestamp: timestamp.Truncate(time.Hour),
	}
	// mid mirrors consensus's MidState: when multiple revisions of the same
	// contract appear in a single block (or a contract is revised and then
	// resolved in the same block, or a renewal-created contract is revised
	// later in the same block), each raw V2FileContractRevision/Resolution
	// carries the *pre-block* Parent.V2FileContract — consensus's
	// validateRevision chains them against its own midstate
	// (core/consensus/validation.go:733-738). Without doing the same chaining
	// here, the Nth event's "existing" values would equal the (N-1)th event's,
	// and the active-state counters would double-debit the parent's prior
	// value.
	mid := make(map[types.FileContractID]types.V2FileContract)
	currentParent := func(id types.FileContractID, fallback types.V2FileContract) types.V2FileContract {
		if fc, ok := mid[id]; ok {
			return fc
		}
		return fallback
	}
	for _, txn := range txns {
		txnID := txn.ID()

		// Fresh formations. txn.FileContracts only contains contracts created
		// from scratch in this transaction; renewal-created contracts come
		// through txn.FileContractResolutions below.
		for i, fc := range txn.FileContracts {
			id := txn.V2FileContractID(txnID, i)
			log := log.With(zap.Stringer("id", id))
			riskedCollateral := subOrZero(fc.TotalCollateral, fc.MissedHostValue)
			riskedHostRevenue := subOrZero(fc.HostOutput.Value, fc.TotalCollateral)
			renterTax := cs.V2FileContractTax(fc)
			state.Formations = append(state.Formations, ContractFormation{
				Host:          fc.HostPublicKey,
				Renter:        fc.RenterPublicKey,
				Size:          fc.Filesize,
				BytesUploaded: fc.Filesize,

				RenterAllowance:     fc.RemainingAllowance(),
				RenterContractPrice: riskedHostRevenue,
				RenterTax:           renterTax,

				HostLockedCollateral: fc.TotalCollateral,
				HostRiskedCollateral: riskedCollateral,
				HostPotentialRevenue: riskedHostRevenue,
			})
			mid[id] = fc
			log.Debug("contract formation", zap.Stringer("host", fc.HostPublicKey), zap.Stringer("renter", fc.RenterPublicKey), zap.Stringer("allowance", fc.RemainingAllowance()), zap.Stringer("contractPrice", riskedHostRevenue), zap.Stringer("tax", renterTax), zap.Stringer("collateral", riskedCollateral), zap.Stringer("revenue", riskedHostRevenue))
		}

		// On-chain revisions. Every revision is tracked, even ones whose
		// resulting state isn't well-formed in the strict
		// MissedHostValue ≤ TotalCollateral ≤ HostOutput.Value sense;
		// subOrZero clamps the derived values to zero in that case.
		// Skipping malformed states would strand the contract: a later
		// revision could bring it back to a well-formed state and the next
		// event would underflow the active counters.
		for _, rev := range txn.FileContractRevisions {
			log := log.With(zap.Stringer("id", rev.Parent.ID))
			fc := currentParent(rev.Parent.ID, rev.Parent.V2FileContract)
			existingRiskedCollateral := subOrZero(fc.TotalCollateral, fc.MissedHostValue)
			newRiskedCollateral := subOrZero(rev.Revision.TotalCollateral, rev.Revision.MissedHostValue)
			existingPotentialRevenue := subOrZero(fc.HostOutput.Value, fc.TotalCollateral)
			newPotentialRevenue := subOrZero(rev.Revision.HostOutput.Value, rev.Revision.TotalCollateral)
			state.Revisions = append(state.Revisions, ContractRevision{
				Host:   fc.HostPublicKey,
				Renter: fc.RenterPublicKey,

				ExistingSize: fc.Filesize,
				NewSize:      rev.Revision.Filesize,

				ExistingAllowance: fc.RemainingAllowance(),
				NewAllowance:      rev.Revision.RemainingAllowance(),

				ExistingRiskedCollateral: existingRiskedCollateral,
				NewRiskedCollateral:      newRiskedCollateral,

				ExistingPotentialRevenue: existingPotentialRevenue,
				NewPotentialRevenue:      newPotentialRevenue,
			})
			mid[rev.Parent.ID] = rev.Revision
			log.Debug("contract revision", zap.Uint64("revisionNumber", rev.Revision.RevisionNumber), zap.Stringer("allowance", rev.Revision.RemainingAllowance()), zap.Stringer("existingRisked", existingRiskedCollateral), zap.Stringer("newRisked", newRiskedCollateral), zap.Stringer("revenue", newPotentialRevenue), zap.Stringer("host", fc.HostPublicKey), zap.Stringer("renter", fc.RenterPublicKey))
		}

		// Resolutions. Renewals also emit the new contract's formation here,
		// inline, so FromRenewal can be set with certainty rather than via a
		// cross-reference lookup.
		for _, fcr := range txn.FileContractResolutions {
			log := log.With(zap.Stringer("id", fcr.Parent.ID))
			fc := currentParent(fcr.Parent.ID, fcr.Parent.V2FileContract)
			parentRiskedHostRevenue := subOrZero(fc.HostOutput.Value, fc.TotalCollateral)
			cr := ContractResolution{
				Host:   fc.HostPublicKey,
				Renter: fc.RenterPublicKey,
				Size:   fc.Filesize,

				RenterAllowance: fc.RemainingAllowance(),

				HostLockedCollateral: fc.TotalCollateral,
				HostRiskedCollateral: subOrZero(fc.TotalCollateral, fc.MissedHostValue),
				HostPotentialRevenue: parentRiskedHostRevenue,
			}

			switch res := fcr.Resolution.(type) {
			case *types.V2FileContractExpiration:
				cr.Type = ResolutionTypeExpired
				cr.HostBurn = subOrZero(fc.HostOutput.Value, fc.MissedHostValue)
			case *types.V2StorageProof:
				cr.Type = ResolutionTypeProof
				cr.HostEarnedRevenue = parentRiskedHostRevenue
			case *types.V2FileContractRenewal:
				cr.Type = ResolutionTypeRenewed
				// Refreshes carry old host revenue forward into the renewal
				// contract; duration-extending renewals settle the old term.
				// Revenue that remains potential in the new contract is not
				// earned here.
				newRevenue, earnedRevenue := renewalRevenue(fc, res)
				cr.HostEarnedRevenue = earnedRevenue
				// The renter's off-chain spend on the parent surfaces here
				// as the gap between latest off-chain RiskedHostRevenue and
				// the on-chain parent. subOrZero handles two consensus-
				// permitted edge cases: (1) a parent whose TC > HostOutput.Value
				// (post-revision redistribution), where the first subtraction
				// would otherwise underflow, and (2) a renewal whose per-side
				// values regress from on-chain (rhp4 doesn't produce this).
				finalHostValue := res.FinalHostOutput.Value.Add(res.HostRollover)
				latestPotentialRevenue := subOrZero(finalHostValue, fc.TotalCollateral)
				cr.RenterDeferredSpend = subOrZero(latestPotentialRevenue, parentRiskedHostRevenue)

				// Emit the renewal-created formation alongside the resolution.
				// BytesUploaded credits only the off-chain growth surfaced
				// here: newFC.Filesize is the latest off-chain size (per
				// rhp4 NewContract.Filesize = fc.Filesize at construction),
				// while the parent's on-chain Filesize may be stale (no
				// broadcast revision). The delta is real upload activity
				// that's never been counted; without crediting it here,
				// BytesUploaded drifts below ActiveSize over renewals.
				newFC := res.NewContract
				newID := fcr.Parent.ID.V2RenewalID()
				mid[newID] = newFC
				newRiskedCollateral := subOrZero(newFC.TotalCollateral, newFC.MissedHostValue)
				newRiskedHostRevenue := subOrZero(newFC.HostOutput.Value, newFC.TotalCollateral)
				var renewalBytesUploaded uint64
				if newFC.Filesize > fc.Filesize {
					renewalBytesUploaded = newFC.Filesize - fc.Filesize
				}
				renewalLog := log.With(zap.Stringer("renewalID", newID), zap.Uint64("renewalProofHeight", newFC.ProofHeight))
				state.Formations = append(state.Formations, ContractFormation{
					Host:          newFC.HostPublicKey,
					Renter:        newFC.RenterPublicKey,
					Size:          newFC.Filesize,
					BytesUploaded: renewalBytesUploaded,

					RenterAllowance:     newFC.RemainingAllowance(),
					RenterContractPrice: newRevenue,
					RenterTax:           cs.V2FileContractTax(newFC),

					HostLockedCollateral: newFC.TotalCollateral,
					HostRiskedCollateral: newRiskedCollateral,
					HostPotentialRevenue: newRiskedHostRevenue,
				})
				renewalLog.Debug("renewal-created formation", zap.Stringer("host", newFC.HostPublicKey), zap.Stringer("renter", newFC.RenterPublicKey), zap.Stringer("allowance", newFC.RemainingAllowance()), zap.Stringer("contractPrice", newRevenue), zap.Stringer("collateral", newRiskedCollateral), zap.Stringer("revenue", newRiskedHostRevenue), zap.Uint64("bytesUploaded", renewalBytesUploaded))
			default:
				panic(fmt.Sprintf("unknown resolution type: %T", fcr.Resolution)) // should never happen
			}

			state.Resolutions = append(state.Resolutions, cr)
			log.Debug("contract resolution", zap.Stringer("host", fc.HostPublicKey), zap.Stringer("renter", fc.RenterPublicKey), zap.Stringer("allowance", fc.RemainingAllowance()), zap.Stringer("collateral", fc.RiskedCollateral()), zap.Stringer("revenue", fc.RiskedHostRevenue()))
		}
	}
	return state, nil
}

func (m *Manager) indexState(ctx context.Context, tip types.ChainIndex) (types.ChainIndex, error) {
	for {
		if ctx.Err() != nil {
			return types.ChainIndex{}, ctx.Err()
		}

		reverted, applied, err := m.chain.UpdatesSince(tip, 100)
		if err != nil {
			return types.ChainIndex{}, fmt.Errorf("failed to get updates since last tip: %w", err)
		} else if len(reverted) == 0 && len(applied) == 0 {
			return tip, nil
		}

		for _, cru := range reverted {
			revertIndex := types.ChainIndex{
				Height: cru.State.Index.Height + 1,
				ID:     cru.Block.ID(),
			}
			log := m.log.With(zap.Stringer("index", revertIndex)).Named("revert")
			state, err := parseDiffs(cru.Block.Timestamp.Truncate(time.Hour), cru.State, cru.Block.V2Transactions(), log)
			if err != nil {
				return types.ChainIndex{}, fmt.Errorf("failed to parse reverted diffs: %w", err)
			}
			state.V2TransactionCount = uint64(len(cru.Block.V2Transactions()))
			state.BlockInterval = cru.State.Network.BlockInterval
			if err := m.store.RevertState(ctx, cru.State.Index, state); err != nil {
				return types.ChainIndex{}, fmt.Errorf("failed to revert state: %w", err)
			}
			tip = cru.State.Index
			log.Debug("reverted state")
		}

		for _, cau := range applied {
			timestamp := cau.Block.Timestamp.Truncate(time.Hour)
			log := m.log.With(zap.Stringer("index", cau.State.Index), zap.Time("timestamp", timestamp)).Named("apply")
			state, err := parseDiffs(timestamp, cau.State, cau.Block.V2Transactions(), log)
			if err != nil {
				return types.ChainIndex{}, fmt.Errorf("failed to parse applied diffs: %w", err)
			}
			state.V2TransactionCount = uint64(len(cau.Block.V2Transactions()))
			state.BlockInterval = cau.State.Network.BlockInterval
			if err := m.store.ApplyState(ctx, cau.State.Index, state); err != nil {
				return types.ChainIndex{}, fmt.Errorf("failed to apply state: %w", err)
			}
			tip = cau.State.Index
			log.Debug("applied state")
		}

		// Pruning is opt-in (0 = disabled). When enabled, drop block bodies
		// older than the configured retention window from the consensus
		// database.
		if m.pruneRetentionBlocks > 0 && tip.Height > m.pruneRetentionBlocks {
			m.chain.PruneBlocks(tip.Height - m.pruneRetentionBlocks)
		}
	}
}

// RenterMetric retrieves the latest metrics for a renter.
func (m *Manager) RenterMetric(ctx context.Context, renterKey types.PublicKey, timestamp time.Time) (Renter, error) {
	return m.store.RenterMetric(ctx, renterKey, timestamp)
}

// HostMetric retrieves the latest metrics for a host.
func (m *Manager) HostMetric(ctx context.Context, hostKey types.PublicKey, timestamp time.Time) (Host, error) {
	return m.store.HostMetric(ctx, hostKey, timestamp)
}

// GlobalMetric retrieves the latest metrics for the global state.
func (m *Manager) GlobalMetric(ctx context.Context, timestamp time.Time) (Metrics, error) {
	return m.store.GlobalMetric(ctx, timestamp)
}

// RenterMetrics retrieves metrics for a renter over a time range.
func (m *Manager) RenterMetrics(ctx context.Context, renterKey types.PublicKey, start, end time.Time) ([]Renter, error) {
	return m.store.RenterMetrics(ctx, renterKey, start, end)
}

// HostMetrics retrieves metrics for a host over a time range.
func (m *Manager) HostMetrics(ctx context.Context, hostKey types.PublicKey, start, end time.Time) ([]Host, error) {
	return m.store.HostMetrics(ctx, hostKey, start, end)
}

// GlobalMetrics retrieves global metrics over a time range.
func (m *Manager) GlobalMetrics(ctx context.Context, start, end time.Time) ([]Metrics, error) {
	return m.store.GlobalMetrics(ctx, start, end)
}

// TopHosts retrieves the top hosts based on their metrics over a time range.
func (m *Manager) TopHosts(ctx context.Context, start, end time.Time, limit int) ([]Host, error) {
	return m.store.TopHosts(ctx, start, end, limit)
}

// TopRenters retrieves the top renters based on their metrics over a time range.
func (m *Manager) TopRenters(ctx context.Context, start, end time.Time, limit int) ([]Renter, error) {
	return m.store.TopRenters(ctx, start, end, limit)
}

// TopHostsBySize retrieves the top hosts ranked by their most recent
// active_size in [start, end].
func (m *Manager) TopHostsBySize(ctx context.Context, start, end time.Time, limit int) ([]Host, error) {
	return m.store.TopHostsBySize(ctx, start, end, limit)
}

// TopRentersBySize retrieves the top renters ranked by their most recent
// active_size in [start, end].
func (m *Manager) TopRentersBySize(ctx context.Context, start, end time.Time, limit int) ([]Renter, error) {
	return m.store.TopRentersBySize(ctx, start, end, limit)
}

// HostsCount retrieves the count of hosts over a time range.
func (m *Manager) HostsCount(ctx context.Context, start, end time.Time) (int64, error) {
	return m.store.HostsCount(ctx, start, end)
}

// RentersCount retrieves the count of renters over a time range.
func (m *Manager) RentersCount(ctx context.Context, start, end time.Time) (int64, error) {
	return m.store.RentersCount(ctx, start, end)
}

// NewHosts retrieves the count of hosts whose first recorded event falls in
// the given time range. Use day boundaries for "new hosts per day".
func (m *Manager) NewHosts(ctx context.Context, start, end time.Time) (int64, error) {
	return m.store.NewHosts(ctx, start, end)
}

// NewRenters retrieves the count of renters whose first recorded event falls
// in the given time range. Use day boundaries for "new renters per day".
func (m *Manager) NewRenters(ctx context.Context, start, end time.Time) (int64, error) {
	return m.store.NewRenters(ctx, start, end)
}

// Close stops the manager and cleans up resources.
func (m *Manager) Close() error {
	m.tg.Stop()
	return nil
}

// WithPruneRetentionBlocks configures how many recent blocks the chain manager
// is asked to retain after each indexing pass. Older blocks are dropped from
// the consensus database via Chain.PruneBlocks. A value of 0 (the default)
// disables pruning entirely.
func WithPruneRetentionBlocks(blocks uint64) ManagerOption {
	return func(m *Manager) {
		m.pruneRetentionBlocks = blocks
	}
}

// NewManager creates a new metrics manager that indexes the chain state and provides access to metrics.
func NewManager(chain Chain, store Store, log *zap.Logger, opts ...ManagerOption) (*Manager, error) {
	m := &Manager{
		chain: chain,
		store: store,
		log:   log.Named("metrics"),
		tg:    threadgroup.New(),
	}
	for _, opt := range opts {
		opt(m)
	}

	ctx, cancel, err := m.tg.AddContext(context.Background())
	if err != nil {
		return nil, err
	}

	tip, err := m.store.LastIndexedTip(ctx)
	if err != nil && !errors.Is(err, ErrTipNotFound) {
		cancel()
		return nil, fmt.Errorf("failed to get last indexed tip: %w", err)
	}

	go func() {
		defer cancel()

		reorgCh := make(chan struct{}, 1)
		reorgCh <- struct{}{} // initial trigger
		unsubscribe := m.chain.OnReorg(func(tip types.ChainIndex) {
			select {
			case reorgCh <- struct{}{}:
			default:
			}
		})
		defer unsubscribe()

		for {
			select {
			case <-ctx.Done():
				return
			case <-reorgCh:
			}

			tip, err = m.indexState(ctx, tip)
			if errors.Is(err, context.Canceled) {
				return
			} else if err != nil {
				m.log.Panic("failed to index state", zap.Error(err))
			}
		}
	}()

	return m, nil
}
