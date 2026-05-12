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

const blockPruneDays = 7

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
		// contracts — the upfront contract price at each formation plus
		// the allowance consumed by storage revisions. It does not
		// include Tax or miner fees.
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
		// RolledRevenue is the portion of HostRollover that represents
		// previously-accrued revenue rather than original collateral, i.e.
		// max(0, HostRollover − TotalCollateral). It is zero for standard
		// renewals (where HostRollover ≤ TotalCollateral) and equal to
		// fc.RiskedHostRevenue() for refreshes (where HostRollover absorbs
		// the entire HostOutput.Value). The renewed contract counts this
		// quantity inside its own RiskedHostRevenue and RenterContractPrice
		// at formation; subtracting it here cancels the double-count so
		// that the rolled revenue stays "at risk" rather than being
		// realized at renewal time — if the new contract eventually fails,
		// the rolled revenue is correctly absorbed into HostBurn.
		RolledRevenue types.Currency

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
		tg    *threadgroup.ThreadGroup
		chain Chain
		store Store
		log   *zap.Logger
	}
)

var (
	// ErrTipNotFound is returned when the last indexed tip is not found in the store.
	ErrTipNotFound = fmt.Errorf("tip not found")
	// ErrNotFound is returned when a requested metric is not found.
	ErrNotFound = fmt.Errorf("not found")
)

// wellFormedV2Contract reports whether a v2 file contract fits the standard
// payout decomposition assumed by the metrics: MissedHostValue ≤ TotalCollateral
// ≤ HostOutput.Value. Consensus enforces the second inequality only at
// formation (not on revisions) and never enforces the first, so a hand-crafted
// or non-standard contract — or a contract revised in a way that redistributes
// value from HostOutput.Value to RenterOutput.Value — can validly violate
// either. The metric model relies on both inequalities to avoid Currency.Sub
// underflows in:
//   - fc.RiskedCollateral()       = TotalCollateral − MissedHostValue
//   - fc.RiskedHostRevenue()      = HostOutput.Value − TotalCollateral
//   - HostOutput.Value − MissedHostValue (used as HostBurn at expiration)
//
// Contracts that fail either inequality are skipped for metric purposes.
func wellFormedV2Contract(fc types.V2FileContract) bool {
	return fc.MissedHostValue.Cmp(fc.TotalCollateral) <= 0 &&
		fc.TotalCollateral.Cmp(fc.HostOutput.Value) <= 0
}

func parseDiffs(timestamp time.Time, cs consensus.State, diffs []consensus.V2FileContractElementDiff, log *zap.Logger) (State, error) {
	state := State{
		Timestamp: timestamp.Truncate(time.Hour),
	}
	for _, diff := range diffs {
		log := log.With(zap.Stringer("id", diff.V2FileContractElement.ID))
		fc := diff.V2FileContractElement.V2FileContract
		// Skip contracts that don't fit the standard payout decomposition.
		// Consensus allows MissedHostValue > TotalCollateral, but the metric
		// model can't attribute such a contract's payouts to collateral vs.
		// revenue, and fc.RiskedCollateral() would underflow. Since consensus
		// makes TotalCollateral immutable and only allows MissedHostValue to
		// decrease, a well-formed parent implies a well-formed revision, so
		// checking the parent here covers formations, revisions, and
		// resolutions consistently.
		if !wellFormedV2Contract(fc) {
			log.Warn("skipping malformed v2 contract", zap.Stringer("missedHostValue", fc.MissedHostValue), zap.Stringer("totalCollateral", fc.TotalCollateral), zap.Stringer("hostOutput", fc.HostOutput.Value))
			continue
		}
		if diff.Created {
			// RenterContractPrice is the SC the renter has already paid the
			// host at formation. At a fresh formation this equals the contract
			// price; at a renewal it also includes any pre-paid storage cost
			// that was rolled into HostOutput.Value. Since MissedHostValue ==
			// TotalCollateral at standard formations, this equals
			// fc.RiskedHostRevenue() but is computed explicitly to be robust
			// to non-standard contracts where they differ.
			renterContractPrice := fc.HostOutput.Value.Sub(fc.TotalCollateral)
			renterTax := cs.V2FileContractTax(fc)
			state.Formations = append(state.Formations, ContractFormation{
				Host:   fc.HostPublicKey,
				Renter: fc.RenterPublicKey,
				Size:   fc.Filesize,

				RenterAllowance:     fc.RemainingAllowance(),
				RenterContractPrice: renterContractPrice,
				RenterTax:           renterTax,

				HostLockedCollateral: fc.TotalCollateral,
				HostRiskedCollateral: fc.RiskedCollateral(),
				HostPotentialRevenue: fc.RiskedHostRevenue(),
			})
			log.Debug("contract formation", zap.Stringer("host", fc.HostPublicKey), zap.Stringer("renter", fc.RenterPublicKey), zap.Stringer("allowance", fc.RemainingAllowance()), zap.Stringer("contractPrice", renterContractPrice), zap.Stringer("tax", renterTax), zap.Stringer("collateral", fc.RiskedCollateral()), zap.Stringer("revenue", fc.RiskedHostRevenue()))
		} else if rev, ok := diff.V2RevisionElement(); ok {
			// The parent fc passed wellFormedV2Contract above, but a revision
			// can redistribute HostOutput.Value into RenterOutput.Value (while
			// preserving their sum), which consensus does not check against
			// TotalCollateral. If the revision lands in a state where
			// TotalCollateral > HostOutput.Value, rev.RiskedHostRevenue() would
			// underflow. We can't safely record this revision and won't be
			// able to safely process any subsequent diffs for this contract
			// either (the parent fc on the next block will be malformed and
			// fail the top-of-loop check). Synthesize an abandonment
			// resolution so the contract stops being counted as active. We
			// use the last well-formed state (fc) as the basis for the
			// counters being released; HostBurn and HostEarnedRevenue stay
			// zero because no actual chain settlement occurred.
			if !wellFormedV2Contract(rev.V2FileContract) {
				log.Warn("abandoning v2 contract after malformed revision", zap.Stringer("missedHostValue", rev.V2FileContract.MissedHostValue), zap.Stringer("totalCollateral", rev.V2FileContract.TotalCollateral), zap.Stringer("hostOutput", rev.V2FileContract.HostOutput.Value))
				state.Resolutions = append(state.Resolutions, ContractResolution{
					Host:                 fc.HostPublicKey,
					Renter:               fc.RenterPublicKey,
					Size:                 fc.Filesize,
					Type:                 ResolutionTypeAbandoned,
					RenterAllowance:      fc.RemainingAllowance(),
					HostLockedCollateral: fc.TotalCollateral,
					HostRiskedCollateral: fc.RiskedCollateral(),
					HostPotentialRevenue: fc.RiskedHostRevenue(),
				})
				continue
			}
			state.Revisions = append(state.Revisions, ContractRevision{
				Host:   fc.HostPublicKey,
				Renter: fc.RenterPublicKey,

				ExistingSize: fc.Filesize,
				NewSize:      rev.V2FileContract.Filesize,

				ExistingAllowance: fc.RemainingAllowance(),
				NewAllowance:      rev.V2FileContract.RemainingAllowance(),

				ExistingRiskedCollateral: fc.RiskedCollateral(),
				NewRiskedCollateral:      rev.V2FileContract.RiskedCollateral(),

				ExistingPotentialRevenue: fc.RiskedHostRevenue(),
				NewPotentialRevenue:      rev.V2FileContract.RiskedHostRevenue(),
			})
			log.Debug("contract revision", zap.Uint64("revisionNumber", rev.V2FileContract.RevisionNumber), zap.Stringer("allowance", rev.V2FileContract.RemainingAllowance()), zap.Stringer("existingRisked", fc.RiskedCollateral()), zap.Stringer("newRisked", rev.V2FileContract.RiskedCollateral()), zap.Stringer("revenue", rev.V2FileContract.RiskedHostRevenue()), zap.Stringer("host", fc.HostPublicKey), zap.Stringer("renter", fc.RenterPublicKey))
		} else if res := diff.Resolution; res != nil {
			cr := ContractResolution{
				Host:   fc.HostPublicKey,
				Renter: fc.RenterPublicKey,
				Size:   fc.Filesize,

				RenterAllowance: fc.RemainingAllowance(),

				HostLockedCollateral: fc.TotalCollateral,
				HostRiskedCollateral: fc.RiskedCollateral(),
				HostPotentialRevenue: fc.RiskedHostRevenue(),
			}

			switch res := res.(type) {
			case *types.V2FileContractExpiration:
				cr.Type = ResolutionTypeExpired
				cr.HostBurn = fc.HostOutput.Value.Sub(fc.MissedHostValue)
			case *types.V2StorageProof:
				cr.Type = ResolutionTypeProof
				cr.HostEarnedRevenue = fc.RiskedHostRevenue()
			case *types.V2FileContractRenewal:
				// Consensus runs validateContract on the renewal's new
				// contract (enforcing TotalCollateral ≤ HostOutput.Value)
				// but does not enforce MissedHostValue ≤ TotalCollateral,
				// so the new contract can still fail wellFormedV2Contract.
				// In that case the new contract will be skipped at its
				// own Created diff, leaving the lineage untrackable. We
				// classify this as an abandonment rather than a renewal
				// so we don't credit RenewedContracts for a renewal whose
				// successor we can't account for. Active-counter releases
				// for the old contract still apply (handled in sqlite).
				if !wellFormedV2Contract(res.NewContract) {
					log.Warn("abandoning v2 contract: renewal produced malformed new contract", zap.Stringer("missedHostValue", res.NewContract.MissedHostValue), zap.Stringer("totalCollateral", res.NewContract.TotalCollateral), zap.Stringer("hostOutput", res.NewContract.HostOutput.Value))
					cr.Type = ResolutionTypeAbandoned
					break
				}
				cr.Type = ResolutionTypeRenewed
				// HostRollover is taken from fc.HostOutput.Value, which
				// decomposes as TotalCollateral + RiskedHostRevenue.
				// Treat HostRollover as absorbing collateral first; any
				// excess is previously-accrued revenue being carried into
				// the new contract. For a standard RHP4 renew this is
				// zero (HostRollover ≤ TotalCollateral); for an RHP4
				// refresh HostRollover absorbs the entire HostOutput and
				// this equals fc.RiskedHostRevenue().
				if res.HostRollover.Cmp(fc.TotalCollateral) > 0 {
					cr.RolledRevenue = res.HostRollover.Sub(fc.TotalCollateral)
				}
				// HostEarnedRevenue is what is irrevocably realized to
				// the host at this resolution: the wallet inflow
				// (FinalHostOutput.Value) beyond any of the host's
				// original collateral that's returned to wallet
				// (TotalCollateral − HostRollover, when positive). In a
				// standard renew the rolled-over collateral is fully
				// absorbed, the wallet receives exactly
				// fc.RiskedHostRevenue(), and that becomes the earned
				// revenue. In a refresh the entire RiskedHostRevenue is
				// rolled into the new contract, FinalHostOutput is zero,
				// and earned revenue at this point is zero — the rolled
				// revenue stays at risk and will be recognized later
				// (as EarnedRevenue on a successful proof / renew of
				// the successor, or absorbed into HostBurn if the
				// successor expires with burn).
				var collateralReturned types.Currency
				if fc.TotalCollateral.Cmp(res.HostRollover) > 0 {
					collateralReturned = fc.TotalCollateral.Sub(res.HostRollover)
				}
				if res.FinalHostOutput.Value.Cmp(collateralReturned) > 0 {
					cr.HostEarnedRevenue = res.FinalHostOutput.Value.Sub(collateralReturned)
				}
				// Renter-side telescoping. The rolled-over allowance is
				// removed from Locked here and re-added by the new
				// contract's formation. The new contract's
				// RenterContractPrice (NewHostOutput − NewTotalCollateral)
				// includes RolledRevenue, so without correction Spent
				// would double-count it on every refresh; the sqlite
				// layer subtracts RolledRevenue here to cancel that.
			default:
				panic(fmt.Sprintf("unknown resolution type: %T", res)) // should never happen
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

		var cs consensus.State
		for _, cru := range reverted {
			revertIndex := types.ChainIndex{
				Height: cru.State.Index.Height + 1,
				ID:     cru.Block.ID(),
			}
			log := m.log.With(zap.Stringer("index", revertIndex)).Named("revert")
			state, err := parseDiffs(cru.Block.Timestamp.Truncate(time.Hour), cru.State, cru.V2FileContractElementDiffs(), log)
			if err != nil {
				return types.ChainIndex{}, fmt.Errorf("failed to parse reverted diffs: %w", err)
			}
			state.V2TransactionCount = uint64(len(cru.Block.V2Transactions()))
			state.BlockInterval = cru.State.Network.BlockInterval
			if err := m.store.RevertState(ctx, cru.State.Index, state); err != nil {
				return types.ChainIndex{}, fmt.Errorf("failed to revert state: %w", err)
			}
			tip = cru.State.Index
			cs = cru.State
			log.Debug("reverted state")
		}

		for _, cau := range applied {
			timestamp := cau.Block.Timestamp.Truncate(time.Hour)
			log := m.log.With(zap.Stringer("index", cau.State.Index), zap.Time("timestamp", timestamp)).Named("apply")
			state, err := parseDiffs(timestamp, cau.State, cau.V2FileContractElementDiffs(), log)
			if err != nil {
				return types.ChainIndex{}, fmt.Errorf("failed to parse applied diffs: %w", err)
			}
			state.V2TransactionCount = uint64(len(cau.Block.V2Transactions()))
			state.BlockInterval = cau.State.Network.BlockInterval
			if err := m.store.ApplyState(ctx, cau.State.Index, state); err != nil {
				return types.ChainIndex{}, fmt.Errorf("failed to apply state: %w", err)
			}
			tip = cau.State.Index
			cs = cau.State
			log.Debug("applied state")
		}

		blocksPerDay := uint64((24 * time.Hour) / cs.Network.BlockInterval)
		pruneTarget := blocksPerDay * blockPruneDays
		if tip.Height > pruneTarget {
			m.chain.PruneBlocks(tip.Height - pruneTarget)
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

// NewManager creates a new metrics manager that indexes the chain state and provides access to metrics.
func NewManager(chain Chain, store Store, log *zap.Logger) (*Manager, error) {
	m := &Manager{
		chain: chain,
		store: store,
		log:   log.Named("metrics"),
		tg:    threadgroup.New(),
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
