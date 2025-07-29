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

		ActiveSize uint64 `json:"activeSize"`
		TotalSize  uint64 `json:"totalSize"`

		LockedCollateral types.Currency `json:"lockedCollateral"`
		RiskedCollateral types.Currency `json:"riskedCollateral"`
		BurntCollateral  types.Currency `json:"burntCollateral"`
		PotentialRevenue types.Currency `json:"potentialRevenue"`
		EarnedRevenue    types.Currency `json:"earnedRevenue"`
		Timestamp        time.Time      `json:"timestamp"`
	}

	// A Renter represents the metrics for a renter.
	Renter struct {
		PublicKey types.PublicKey `json:"publicKey"`

		ActiveContracts     uint64 `json:"activeContracts"`
		RenewedContracts    uint64 `json:"renewedContracts"`
		SuccessfulContracts uint64 `json:"successfulContracts"`
		FailedContracts     uint64 `json:"failedContracts"`

		ActiveSize uint64 `json:"activeSize"`
		TotalSize  uint64 `json:"totalSize"`

		Spent     types.Currency `json:"spent"`
		Locked    types.Currency `json:"locked"`
		Timestamp time.Time      `json:"timestamp"`
	}

	// Metrics represents the global metrics.
	Metrics struct {
		Renters uint64 `json:"renters"`
		Hosts   uint64 `json:"hosts"`

		ActiveContracts     uint64 `json:"activeContracts"`
		RenewedContracts    uint64 `json:"renewedContracts"`
		SuccessfulContracts uint64 `json:"successfulContracts"`
		FailedContracts     uint64 `json:"failedContracts"`

		ActiveSize uint64 `json:"activeSize"`
		TotalSize  uint64 `json:"totalSize"`

		SpentAllowance  types.Currency `json:"spentAllowance"`
		LockedAllowance types.Currency `json:"lockedAllowance"`

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

		RenterAllowance      types.Currency
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

		RenterAllowance types.Currency
		RenterSpent     types.Currency

		HostLockedCollateral types.Currency
		HostRiskedCollateral types.Currency
		HostBurn             types.Currency
		HostPotentialRevenue types.Currency
		HostEarnedRevenue    types.Currency
	}

	// A State represents the state of the chain at a specific timestamp.
	State struct {
		Timestamp time.Time `json:"timestamp"`

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

func parseDiffs(timestamp time.Time, diffs []consensus.V2FileContractElementDiff, log *zap.Logger) (State, error) {
	state := State{
		Timestamp: timestamp.Truncate(time.Hour),
	}
	for _, diff := range diffs {
		log := log.With(zap.Stringer("id", diff.V2FileContractElement.ID))
		fc := diff.V2FileContractElement.V2FileContract
		if diff.Created {
			state.Formations = append(state.Formations, ContractFormation{
				Host:   fc.HostPublicKey,
				Renter: fc.RenterPublicKey,
				Size:   fc.Filesize,

				RenterAllowance: fc.RemainingAllowance(),

				HostLockedCollateral: fc.TotalCollateral,
				HostRiskedCollateral: fc.RiskedCollateral(),
				HostPotentialRevenue: fc.RiskedHostRevenue(),
			})
			log.Debug("contract formation", zap.Stringer("host", fc.HostPublicKey), zap.Stringer("renter", fc.RenterPublicKey), zap.Stringer("allowance", fc.RemainingAllowance()), zap.Stringer("collateral", fc.RiskedCollateral()), zap.Stringer("revenue", fc.RiskedHostRevenue()))
		} else if rev, ok := diff.V2RevisionElement(); ok {
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
				cr.Type = ResolutionTypeRenewed

				if res.FinalHostOutput.Value.Cmp(fc.TotalCollateral) > 0 {
					cr.HostEarnedRevenue = res.FinalHostOutput.Value.Sub(fc.TotalCollateral)
				}

				remainingAllowance := res.FinalRenterOutput.Value.Add(res.RenterRollover)
				if remainingAllowance.Cmp(fc.RemainingAllowance()) < 0 {
					cr.RenterSpent = fc.RemainingAllowance().Sub(remainingAllowance)
				}
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

		for _, cru := range reverted {
			revertIndex := types.ChainIndex{
				Height: cru.State.Index.Height + 1,
				ID:     cru.Block.ID(),
			}
			log := m.log.With(zap.Stringer("index", revertIndex)).Named("revert")
			state, err := parseDiffs(cru.Block.Timestamp.Truncate(time.Hour), cru.V2FileContractElementDiffs(), log)
			if err != nil {
				return types.ChainIndex{}, fmt.Errorf("failed to parse reverted diffs: %w", err)
			} else if err := m.store.RevertState(ctx, cru.State.Index, state); err != nil {
				return types.ChainIndex{}, fmt.Errorf("failed to revert state: %w", err)
			}
			tip = cru.State.Index
			log.Debug("reverted state")
		}

		for _, cau := range applied {
			timestamp := cau.Block.Timestamp.Truncate(time.Hour)
			log := m.log.With(zap.Stringer("index", cau.State.Index), zap.Time("timestamp", timestamp)).Named("apply")
			state, err := parseDiffs(timestamp, cau.V2FileContractElementDiffs(), log)
			if err != nil {
				return types.ChainIndex{}, fmt.Errorf("failed to parse applied diffs: %w", err)
			} else if err := m.store.ApplyState(ctx, cau.State.Index, state); err != nil {
				return types.ChainIndex{}, fmt.Errorf("failed to apply state: %w", err)
			}
			tip = cau.State.Index
			log.Debug("applied state")
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
