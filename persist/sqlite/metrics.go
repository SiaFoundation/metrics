package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go.sia.tech/core/types"
	"go.sia.tech/metrics/metrics"
	"go.uber.org/zap"
)

// secondsPerDay is used as the denominator when accumulating byte-days from
// per-block (activeSize, blockInterval) contributions.
const secondsPerDay = 24 * 60 * 60

// byteDaysDelta returns the byte-days attributable to a single block holding
// `activeSize` bytes for `blockInterval` of time. It approximates the integral
// of ActiveSize over time as a Riemann sum (one rectangle per block) at
// second-granularity; sub-second block intervals truncate to zero.
func byteDaysDelta(activeSize uint64, blockInterval time.Duration) uint64 {
	seconds := uint64(blockInterval / time.Second)
	if seconds == 0 {
		return 0
	}
	return activeSize * seconds / secondsPerDay
}

// LastIndexedTip retrieves the last indexed tip from the store.
func (s *Store) LastIndexedTip(ctx context.Context) (index types.ChainIndex, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		var s sql.Null[[]byte]
		err = tx.QueryRowContext(ctx, "SELECT last_index FROM global_settings").Scan(&s)
		if !s.Valid {
			return metrics.ErrTipNotFound
		}
		dec := types.NewBufDecoder(s.V)
		index.DecodeFrom(dec)
		return dec.Err()
	})
	return
}

// RevertState reverts the store to a previous state based on the provided tip and state.
func (s *Store) RevertState(ctx context.Context, tip types.ChainIndex, state metrics.State) error {
	return s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		// Reverse the block-level updates from ApplyState before processing
		// the event-level reverts. At this point gm.ActiveSize still reflects
		// the post-block state (set by the apply we are reverting), which is
		// the same value ApplyState used when accumulating byte-days, so the
		// subtraction is symmetric.
		gm, err := getMetrics(ctx, tx, state.Timestamp)
		if err != nil {
			return fmt.Errorf("failed to get global metrics: %w", err)
		}
		gm.TransactionCount -= state.V2TransactionCount
		gm.ActiveByteDays -= byteDaysDelta(gm.ActiveSize, state.BlockInterval)
		if err := insertMetrics(ctx, tx, gm); err != nil {
			return fmt.Errorf("failed to insert global metrics: %w", err)
		}

		for _, formation := range state.Formations {
			gm, err := getMetrics(ctx, tx, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get global metrics: %w", err)
			}
			gm.ActiveContracts--
			gm.LockedAllowance = gm.LockedAllowance.Sub(formation.RenterAllowance)
			gm.SpentAllowance = gm.SpentAllowance.Sub(formation.RenterContractPrice)
			gm.Tax = gm.Tax.Sub(formation.RenterTax)
			gm.PotentialRevenue = gm.PotentialRevenue.Sub(formation.HostPotentialRevenue)
			gm.LockedCollateral = gm.LockedCollateral.Sub(formation.HostLockedCollateral)
			gm.RiskedCollateral = gm.RiskedCollateral.Sub(formation.HostRiskedCollateral)
			gm.BytesUploaded -= formation.Size

			hm, err := getHostMetrics(ctx, tx, formation.Host, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get host metrics for %q: %w", formation.Host, err)
			}
			hm.ActiveContracts--
			hm.ActiveSize -= formation.Size
			hm.TotalSize -= formation.Size
			hm.BytesUploaded -= formation.Size
			hm.LockedCollateral = hm.LockedCollateral.Sub(formation.HostLockedCollateral)
			hm.RiskedCollateral = hm.RiskedCollateral.Sub(formation.HostRiskedCollateral)
			hm.PotentialRevenue = hm.PotentialRevenue.Sub(formation.HostPotentialRevenue)

			rm, err := getRenterMetrics(ctx, tx, formation.Renter, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get renter metrics for %q: %w", formation.Renter, err)
			}
			rm.ActiveContracts--
			rm.ActiveSize -= formation.Size
			rm.TotalSize -= formation.Size
			rm.BytesUploaded -= formation.Size
			rm.Locked = rm.Locked.Sub(formation.RenterAllowance)
			rm.Spent = rm.Spent.Sub(formation.RenterContractPrice)
			rm.Tax = rm.Tax.Sub(formation.RenterTax)

			if err := insertMetrics(ctx, tx, gm); err != nil {
				return fmt.Errorf("failed to insert global metrics: %w", err)
			} else if err := insertHostMetrics(ctx, tx, hm); err != nil {
				return fmt.Errorf("failed to insert host metrics for %q: %w", hm.PublicKey, err)
			} else if err := insertRenterMetrics(ctx, tx, rm); err != nil {
				return fmt.Errorf("failed to insert renter metrics for %q: %w", rm.PublicKey, err)
			}
		}

		for _, revision := range state.Revisions {
			spent, ok := revision.ExistingAllowance.SubWithUnderflow(revision.NewAllowance)
			if !ok {
				spent = types.ZeroCurrency
			}
			// Mirror the Apply-side guard: only grow revisions contributed
			// to BytesUploaded, so we only undo that contribution on revert.
			var grewBy uint64
			if revision.NewSize > revision.ExistingSize {
				grewBy = revision.NewSize - revision.ExistingSize
			}

			hm, err := getHostMetrics(ctx, tx, revision.Host, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get host metrics for %q: %w", revision.Host, err)
			}
			hm.RiskedCollateral = hm.RiskedCollateral.Sub(revision.NewRiskedCollateral).Add(revision.ExistingRiskedCollateral)
			hm.PotentialRevenue = hm.PotentialRevenue.Sub(revision.NewPotentialRevenue).Add(revision.ExistingPotentialRevenue)
			hm.ActiveSize = hm.ActiveSize - revision.NewSize + revision.ExistingSize
			hm.TotalSize = hm.TotalSize - revision.NewSize + revision.ExistingSize
			hm.BytesUploaded -= grewBy
			hm.RevisionCount--

			rm, err := getRenterMetrics(ctx, tx, revision.Renter, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get renter metrics for %q: %w", revision.Renter, err)
			}
			rm.ActiveSize = rm.ActiveSize - revision.NewSize + revision.ExistingSize
			rm.TotalSize = rm.TotalSize - revision.NewSize + revision.ExistingSize
			rm.BytesUploaded -= grewBy
			rm.Locked = rm.Locked.Sub(revision.NewAllowance).Add(revision.ExistingAllowance)
			rm.Spent = rm.Spent.Sub(spent)
			rm.RevisionCount--

			gm, err := getMetrics(ctx, tx, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get global metrics: %w", err)
			}
			gm.ActiveSize = gm.ActiveSize - revision.NewSize + revision.ExistingSize
			gm.TotalSize = gm.TotalSize - revision.NewSize + revision.ExistingSize
			gm.BytesUploaded -= grewBy
			gm.LockedAllowance = gm.LockedAllowance.Sub(revision.NewAllowance).Add(revision.ExistingAllowance)
			gm.PotentialRevenue = gm.PotentialRevenue.Sub(revision.NewPotentialRevenue).Add(revision.ExistingPotentialRevenue)
			gm.RiskedCollateral = gm.RiskedCollateral.Sub(revision.NewRiskedCollateral).Add(revision.ExistingRiskedCollateral)
			gm.SpentAllowance = gm.SpentAllowance.Sub(spent)
			gm.RevisionCount--

			if err := insertMetrics(ctx, tx, gm); err != nil {
				return fmt.Errorf("failed to insert global metrics: %w", err)
			} else if err := insertHostMetrics(ctx, tx, hm); err != nil {
				return fmt.Errorf("failed to insert host metrics for %q: %w", hm.PublicKey, err)
			} else if err := insertRenterMetrics(ctx, tx, rm); err != nil {
				return fmt.Errorf("failed to insert renter metrics for %q: %w", rm.PublicKey, err)
			}
		}

		for _, resolution := range state.Resolutions {
			hm, err := getHostMetrics(ctx, tx, resolution.Host, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get host metrics for %q: %w", resolution.Host, err)
			}
			hm.ActiveContracts++
			hm.ActiveSize += resolution.Size
			hm.EarnedRevenue = hm.EarnedRevenue.Sub(resolution.HostEarnedRevenue)
			hm.BurntCollateral = hm.BurntCollateral.Sub(resolution.HostBurn)
			hm.LockedCollateral = hm.LockedCollateral.Add(resolution.HostLockedCollateral)
			hm.RiskedCollateral = hm.RiskedCollateral.Add(resolution.HostRiskedCollateral)
			hm.PotentialRevenue = hm.PotentialRevenue.Add(resolution.HostPotentialRevenue)

			rm, err := getRenterMetrics(ctx, tx, resolution.Renter, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get renter metrics for %q: %w", resolution.Renter, err)
			}
			rm.ActiveContracts++
			rm.ActiveSize += resolution.Size
			rm.Locked = rm.Locked.Add(resolution.RenterAllowance)
			rm.Spent = rm.Spent.Add(resolution.RolledRevenue)

			gm, err := getMetrics(ctx, tx, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get global metrics: %w", err)
			}
			gm.ActiveContracts++
			gm.LockedAllowance = gm.LockedAllowance.Add(resolution.RenterAllowance)
			gm.SpentAllowance = gm.SpentAllowance.Add(resolution.RolledRevenue)
			gm.EarnedRevenue = gm.EarnedRevenue.Sub(resolution.HostEarnedRevenue)
			gm.BurntCollateral = gm.BurntCollateral.Sub(resolution.HostBurn)
			gm.PotentialRevenue = gm.PotentialRevenue.Add(resolution.HostPotentialRevenue)
			gm.LockedCollateral = gm.LockedCollateral.Add(resolution.HostLockedCollateral)
			gm.RiskedCollateral = gm.RiskedCollateral.Add(resolution.HostRiskedCollateral)

			switch resolution.Type {
			case metrics.ResolutionTypeProof:
				hm.SuccessfulContracts--
				rm.SuccessfulContracts--
				gm.SuccessfulContracts--
			case metrics.ResolutionTypeExpired:
				// Mirror the Apply classification: check this resolution's
				// HostBurn, not the host's cumulative BurntCollateral.
				if resolution.HostBurn.IsZero() {
					hm.SuccessfulContracts--
					rm.SuccessfulContracts--
					gm.SuccessfulContracts--
				} else {
					hm.FailedContracts--
					rm.FailedContracts--
					gm.FailedContracts--
				}
			case metrics.ResolutionTypeRenewed:
				hm.RenewedContracts--
				rm.RenewedContracts--
				gm.RenewedContracts--
			}

			if err := insertHostMetrics(ctx, tx, hm); err != nil {
				return fmt.Errorf("failed to insert host metrics for %q: %w", hm.PublicKey, err)
			} else if err := insertRenterMetrics(ctx, tx, rm); err != nil {
				return fmt.Errorf("failed to insert renter metrics for %q: %w", rm.PublicKey, err)
			} else if err := insertMetrics(ctx, tx, gm); err != nil {
				return fmt.Errorf("failed to insert global metrics: %w", err)
			}
		}

		_, err = tx.Exec(ctx, `UPDATE global_settings SET last_index = $1;`, encodable(tip))
		return err
	})
}

// ApplyState applies the provided state to the store, updating the metrics accordingly.
func (s *Store) ApplyState(ctx context.Context, tip types.ChainIndex, state metrics.State) error {
	return s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		for _, formation := range state.Formations {
			gm, err := getMetrics(ctx, tx, state.Timestamp)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("failed to get global metrics: %w", err)
			}
			gm.ActiveContracts++
			gm.ActiveSize += formation.Size
			gm.TotalSize += formation.Size
			gm.BytesUploaded += formation.Size
			gm.LockedAllowance = gm.LockedAllowance.Add(formation.RenterAllowance)
			gm.SpentAllowance = gm.SpentAllowance.Add(formation.RenterContractPrice)
			gm.Tax = gm.Tax.Add(formation.RenterTax)
			gm.PotentialRevenue = gm.PotentialRevenue.Add(formation.HostPotentialRevenue)
			gm.LockedCollateral = gm.LockedCollateral.Add(formation.HostLockedCollateral)
			gm.RiskedCollateral = gm.RiskedCollateral.Add(formation.HostRiskedCollateral)

			hm, err := getHostMetrics(ctx, tx, formation.Host, state.Timestamp)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("failed to get host metrics for %q: %w", formation.Host, err)
			} else if errors.Is(err, sql.ErrNoRows) {
				gm.Hosts++ // increment host count if not found
			}
			s.log.Debug("updating host metrics for formation", zap.Stringer("host", hm.PublicKey), zap.Any("before", hm))
			hm.ActiveContracts++
			hm.ActiveSize += formation.Size
			hm.TotalSize += formation.Size
			hm.BytesUploaded += formation.Size
			hm.LockedCollateral = hm.LockedCollateral.Add(formation.HostLockedCollateral)
			hm.RiskedCollateral = hm.RiskedCollateral.Add(formation.HostRiskedCollateral)
			hm.PotentialRevenue = hm.PotentialRevenue.Add(formation.HostPotentialRevenue)
			if hm.FirstSeen.IsZero() {
				hm.FirstSeen = state.Timestamp
			}
			hm.LastActive = state.Timestamp
			s.log.Debug("updating host metrics for formation", zap.Stringer("host", hm.PublicKey), zap.Any("after", hm))

			rm, err := getRenterMetrics(ctx, tx, formation.Renter, state.Timestamp)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("failed to get renter metrics for %q: %w", formation.Renter, err)
			} else if errors.Is(err, sql.ErrNoRows) {
				gm.Renters++ // increment renter count if not found
			}
			s.log.Debug("updating renter metrics for formation", zap.Stringer("renter", rm.PublicKey), zap.Any("before", rm))
			rm.ActiveContracts++
			rm.ActiveSize += formation.Size
			rm.TotalSize += formation.Size
			rm.BytesUploaded += formation.Size
			rm.Locked = rm.Locked.Add(formation.RenterAllowance)
			rm.Spent = rm.Spent.Add(formation.RenterContractPrice)
			rm.Tax = rm.Tax.Add(formation.RenterTax)
			if rm.FirstSeen.IsZero() {
				rm.FirstSeen = state.Timestamp
			}
			rm.LastActive = state.Timestamp
			s.log.Debug("updating renter metrics for formation", zap.Stringer("renter", rm.PublicKey), zap.Any("after", rm))

			if err := insertMetrics(ctx, tx, gm); err != nil {
				return fmt.Errorf("failed to insert global metrics: %w", err)
			} else if err := insertHostMetrics(ctx, tx, hm); err != nil {
				return fmt.Errorf("failed to insert host metrics for %q: %w", hm.PublicKey, err)
			} else if err := insertRenterMetrics(ctx, tx, rm); err != nil {
				return fmt.Errorf("failed to insert renter metrics for %q: %w", rm.PublicKey, err)
			}
		}

		for _, revision := range state.Revisions {
			// Only count positive size deltas toward BytesUploaded so it
			// remains monotonic. Shrinks update Active/TotalSize but not
			// the upload counter.
			var grewBy uint64
			if revision.NewSize > revision.ExistingSize {
				grewBy = revision.NewSize - revision.ExistingSize
			}

			hm, err := getHostMetrics(ctx, tx, revision.Host, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get revision host metrics for %q: %w", revision.Host, err)
			}
			s.log.Debug("updating host metrics for revision", zap.Stringer("host", hm.PublicKey), zap.Stringer("totalRisked", hm.RiskedCollateral), zap.Stringer("newRisked", revision.NewRiskedCollateral), zap.Stringer("existingRisked", revision.ExistingRiskedCollateral), zap.Time("timestamp", state.Timestamp))
			hm.RiskedCollateral = hm.RiskedCollateral.Sub(revision.ExistingRiskedCollateral).Add(revision.NewRiskedCollateral)
			hm.PotentialRevenue = hm.PotentialRevenue.Sub(revision.ExistingPotentialRevenue).Add(revision.NewPotentialRevenue)
			hm.ActiveSize = hm.ActiveSize - revision.ExistingSize + revision.NewSize
			hm.TotalSize = hm.TotalSize - revision.ExistingSize + revision.NewSize
			hm.BytesUploaded += grewBy
			hm.RevisionCount++
			if hm.FirstSeen.IsZero() {
				hm.FirstSeen = state.Timestamp
			}
			hm.LastActive = state.Timestamp
			s.log.Debug("updating host metrics for revision", zap.Stringer("host", hm.PublicKey), zap.Any("after", hm))

			rm, err := getRenterMetrics(ctx, tx, revision.Renter, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get revision renter metrics for %q: %w", revision.Renter, err)
			}
			spent, ok := revision.ExistingAllowance.SubWithUnderflow(revision.NewAllowance)
			if !ok {
				spent = types.ZeroCurrency
			}
			rm.ActiveSize = rm.ActiveSize - revision.ExistingSize + revision.NewSize
			rm.TotalSize = rm.TotalSize - revision.ExistingSize + revision.NewSize
			rm.BytesUploaded += grewBy
			rm.Locked = rm.Locked.Sub(revision.ExistingAllowance).Add(revision.NewAllowance)
			rm.Spent = rm.Spent.Add(spent)
			rm.RevisionCount++
			if rm.FirstSeen.IsZero() {
				rm.FirstSeen = state.Timestamp
			}
			rm.LastActive = state.Timestamp
			s.log.Debug("updating renter metrics for revision", zap.Stringer("renter", rm.PublicKey), zap.Stringer("spent", spent))

			gm, err := getMetrics(ctx, tx, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get global metrics: %w", err)
			}
			gm.ActiveSize = gm.ActiveSize - revision.ExistingSize + revision.NewSize
			gm.TotalSize = gm.TotalSize - revision.ExistingSize + revision.NewSize
			gm.BytesUploaded += grewBy
			gm.LockedAllowance = gm.LockedAllowance.Sub(revision.ExistingAllowance).Add(revision.NewAllowance)
			gm.PotentialRevenue = gm.PotentialRevenue.Sub(revision.ExistingPotentialRevenue).Add(revision.NewPotentialRevenue)
			gm.RiskedCollateral = gm.RiskedCollateral.Sub(revision.ExistingRiskedCollateral).Add(revision.NewRiskedCollateral)
			gm.SpentAllowance = gm.SpentAllowance.Add(spent)
			gm.RevisionCount++

			if err := insertMetrics(ctx, tx, gm); err != nil {
				return fmt.Errorf("failed to insert global metrics: %w", err)
			} else if err := insertHostMetrics(ctx, tx, hm); err != nil {
				return fmt.Errorf("failed to insert host metrics for %q: %w", hm.PublicKey, err)
			} else if err := insertRenterMetrics(ctx, tx, rm); err != nil {
				return fmt.Errorf("failed to insert renter metrics for %q: %w", rm.PublicKey, err)
			}
		}

		for _, resolution := range state.Resolutions {
			hm, err := getHostMetrics(ctx, tx, resolution.Host, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get resolution host metrics for %q: %w", resolution.Host, err)
			}
			s.log.Debug("updating host metrics for resolution", zap.Stringer("host", hm.PublicKey), zap.Any("metrics", hm))
			hm.ActiveContracts--
			hm.ActiveSize -= resolution.Size
			hm.EarnedRevenue = hm.EarnedRevenue.Add(resolution.HostEarnedRevenue)
			hm.BurntCollateral = hm.BurntCollateral.Add(resolution.HostBurn)
			hm.LockedCollateral = hm.LockedCollateral.Sub(resolution.HostLockedCollateral)
			hm.RiskedCollateral = hm.RiskedCollateral.Sub(resolution.HostRiskedCollateral)
			hm.PotentialRevenue = hm.PotentialRevenue.Sub(resolution.HostPotentialRevenue)
			if hm.FirstSeen.IsZero() {
				hm.FirstSeen = state.Timestamp
			}
			hm.LastActive = state.Timestamp

			rm, err := getRenterMetrics(ctx, tx, resolution.Renter, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get resolution renter metrics for %q: %w", resolution.Renter, err)
			}
			s.log.Debug("updating renter metrics for resolution", zap.Stringer("renter", rm.PublicKey), zap.Any("before", rm))
			rm.ActiveContracts--
			rm.ActiveSize -= resolution.Size
			rm.Locked = rm.Locked.Sub(resolution.RenterAllowance)
			// Cancel the rolled-revenue portion of the successor's
			// RenterContractPrice. See ContractResolution.RolledRevenue.
			rm.Spent = rm.Spent.Sub(resolution.RolledRevenue)
			if rm.FirstSeen.IsZero() {
				rm.FirstSeen = state.Timestamp
			}
			rm.LastActive = state.Timestamp
			s.log.Debug("updating renter metrics for resolution", zap.Stringer("renter", rm.PublicKey), zap.Any("after", rm))

			gm, err := getMetrics(ctx, tx, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get global metrics: %w", err)
			}
			gm.ActiveContracts--
			gm.LockedAllowance = gm.LockedAllowance.Sub(resolution.RenterAllowance)
			gm.SpentAllowance = gm.SpentAllowance.Sub(resolution.RolledRevenue)
			gm.EarnedRevenue = gm.EarnedRevenue.Add(resolution.HostEarnedRevenue)
			gm.BurntCollateral = gm.BurntCollateral.Add(resolution.HostBurn)
			gm.PotentialRevenue = gm.PotentialRevenue.Sub(resolution.HostPotentialRevenue)
			gm.LockedCollateral = gm.LockedCollateral.Sub(resolution.HostLockedCollateral)
			gm.RiskedCollateral = gm.RiskedCollateral.Sub(resolution.HostRiskedCollateral)

			switch resolution.Type {
			case metrics.ResolutionTypeProof:
				hm.SuccessfulContracts++
				rm.SuccessfulContracts++
				gm.SuccessfulContracts++
			case metrics.ResolutionTypeExpired:
				// An expiration is "successful" only if the host did not
				// lose any of HostOutput.Value to the void on this contract.
				// We must inspect *this* resolution's HostBurn, not the
				// host's cumulative BurntCollateral — a host with prior
				// burns would otherwise misclassify every later no-burn
				// expiration as a failure for itself, its renters, and the
				// network.
				if resolution.HostBurn.IsZero() {
					hm.SuccessfulContracts++
					rm.SuccessfulContracts++
					gm.SuccessfulContracts++
				} else {
					hm.FailedContracts++
					rm.FailedContracts++
					gm.FailedContracts++
				}
			case metrics.ResolutionTypeRenewed:
				hm.RenewedContracts++
				rm.RenewedContracts++
				gm.RenewedContracts++
			}

			if err := insertHostMetrics(ctx, tx, hm); err != nil {
				return fmt.Errorf("failed to insert host metrics for %q: %w", hm.PublicKey, err)
			} else if err := insertRenterMetrics(ctx, tx, rm); err != nil {
				return fmt.Errorf("failed to insert renter metrics for %q: %w", rm.PublicKey, err)
			} else if err := insertMetrics(ctx, tx, gm); err != nil {
				return fmt.Errorf("failed to insert global metrics: %w", err)
			}
		}

		// Block-level updates run regardless of whether any v2 contract
		// events occurred. We do this last so gm.ActiveSize reflects the
		// post-event value, which is what we use to attribute byte-days
		// to this block's interval. RevertState reads the same gm.ActiveSize
		// at its start, so the subtraction stays symmetric.
		gm, err := getMetrics(ctx, tx, state.Timestamp)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("failed to get global metrics: %w", err)
		}
		gm.TransactionCount += state.V2TransactionCount
		gm.ActiveByteDays += byteDaysDelta(gm.ActiveSize, state.BlockInterval)
		if err := insertMetrics(ctx, tx, gm); err != nil {
			return fmt.Errorf("failed to insert global metrics: %w", err)
		}

		_, err = tx.Exec(ctx, `UPDATE global_settings SET last_index = $1;`, encodable(tip))
		return err
	})
}

// RenterMetric retrieves the latest renter metrics for a given renter key and timestamp.
func (s *Store) RenterMetric(ctx context.Context, renterKey types.PublicKey, timestamp time.Time) (m metrics.Renter, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		row := tx.QueryRow(ctx, `SELECT renter_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, revision_count, active_size, total_size, bytes_uploaded, locked_allowance, spent_allowance, tax, first_seen, last_active, date_created
FROM renter_metrics WHERE renter_key=$1 AND date_created <= $2 ORDER BY date_created DESC LIMIT 1;`, sqlHash256(renterKey), sqlTime(timestamp))

		m, err = scanRenter(row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // no metrics found for this renter
		}
		return err
	})
	return
}

// HostMetric retrieves the latest host metrics for a given host key and timestamp.
func (s *Store) HostMetric(ctx context.Context, hostKey types.PublicKey, timestamp time.Time) (m metrics.Host, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		row := tx.QueryRow(ctx, `SELECT host_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, revision_count, active_size, total_size, bytes_uploaded, burnt_collateral, locked_collateral, risked_collateral, potential_revenue, earned_revenue, first_seen, last_active, date_created
FROM host_metrics WHERE host_key=$1 AND date_created <= $2 ORDER BY date_created DESC LIMIT 1;`, sqlHash256(hostKey), sqlTime(timestamp))
		m, err = scanHost(row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // no metrics found for this host
		}
		return err
	})
	return
}

// GlobalMetric retrieves the latest global metrics for a given timestamp.
func (s *Store) GlobalMetric(ctx context.Context, timestamp time.Time) (m metrics.Metrics, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		row := tx.QueryRow(ctx, `SELECT renters, hosts, active_contracts, renewed_contracts, successful_contracts, failed_contracts, v2_transaction_count, revision_count, active_size, total_size, bytes_uploaded, active_byte_days, spent_allowance, locked_allowance, tax,
potential_revenue, earned_revenue, locked_collateral, risked_collateral, burnt_collateral, date_created
FROM metrics WHERE date_created <= $1 ORDER BY date_created DESC LIMIT 1;`, sqlTime(timestamp))
		m, err = scanMetrics(row)
		return err
	})
	return
}

// HostMetrics retrieves host metrics for a specific host key within a given time range.
func (s *Store) HostMetrics(ctx context.Context, hostKey types.PublicKey, start, end time.Time) (ms []metrics.Host, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		rows, err := tx.Query(ctx, `SELECT host_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, revision_count, active_size, total_size, bytes_uploaded, burnt_collateral, locked_collateral, risked_collateral, potential_revenue, earned_revenue, first_seen, last_active, date_created
FROM host_metrics WHERE host_key=$1 AND date_created BETWEEN $2 AND $3 ORDER BY date_created;`,
			sqlHash256(hostKey), sqlTime(start), sqlTime(end))
		if err != nil {
			return fmt.Errorf("failed to query host metrics: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			m, err := scanHost(rows)
			if err != nil {
				return fmt.Errorf("failed to scan host metrics: %w", err)
			}
			ms = append(ms, m)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("error iterating host metrics rows: %w", err)
		}
		return nil
	})
	return
}

// RenterMetrics retrieves renter metrics for a specific renter key within a given time range.
func (s *Store) RenterMetrics(ctx context.Context, renterKey types.PublicKey, start, end time.Time) (ms []metrics.Renter, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		rows, err := tx.Query(ctx, `SELECT renter_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, revision_count, active_size, total_size, bytes_uploaded, locked_allowance, spent_allowance, tax, first_seen, last_active, date_created
FROM renter_metrics WHERE renter_key=$1 AND date_created BETWEEN $2 AND $3 ORDER BY date_created;`,
			sqlHash256(renterKey), sqlTime(start), sqlTime(end))
		if err != nil {
			return fmt.Errorf("failed to query renter metrics: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			m, err := scanRenter(rows)
			if err != nil {
				return fmt.Errorf("failed to scan renter metrics: %w", err)
			}
			ms = append(ms, m)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("error iterating renter metrics rows: %w", err)
		}
		return nil
	})
	return
}

// GlobalMetrics retrieves global metrics within a specified time range.
func (s *Store) GlobalMetrics(ctx context.Context, start, end time.Time) (ms []metrics.Metrics, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		rows, err := tx.Query(ctx, `SELECT renters, hosts, active_contracts, renewed_contracts, successful_contracts, failed_contracts, v2_transaction_count, revision_count, active_size, total_size, bytes_uploaded, active_byte_days, spent_allowance, locked_allowance, tax,
potential_revenue, earned_revenue, locked_collateral, risked_collateral, burnt_collateral, date_created
FROM metrics WHERE date_created BETWEEN $1 AND $2 ORDER BY date_created;`, sqlTime(start), sqlTime(end))
		if err != nil {
			return fmt.Errorf("failed to query global metrics: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			m, err := scanMetrics(rows)
			if err != nil {
				return fmt.Errorf("failed to scan metrics: %w", err)
			}
			ms = append(ms, m)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("error iterating global metrics rows: %w", err)
		}
		return nil
	})
	return
}

// HostsCount returns the number of unique hosts in the specified time range.
func (s *Store) HostsCount(ctx context.Context, start, end time.Time) (n int64, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		return tx.QueryRow(ctx, `SELECT COUNT(DISTINCT host_key) FROM host_metrics WHERE date_created BETWEEN $1 AND $2;`, sqlTime(start), sqlTime(end)).Scan(&n)
	})
	return
}

// RentersCount returns the number of unique renters in the specified time range.
func (s *Store) RentersCount(ctx context.Context, start, end time.Time) (n int64, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		return tx.QueryRow(ctx, `SELECT COUNT(DISTINCT renter_key) FROM renter_metrics WHERE date_created BETWEEN $1 AND $2;`, sqlTime(start), sqlTime(end)).Scan(&n)
	})
	return
}

// NewHosts returns the count of distinct hosts whose first recorded event
// (first_seen) falls in [start, end]. first_seen is constant per host across
// snapshots, so DISTINCT on host_key collapses the per-snapshot rows.
func (s *Store) NewHosts(ctx context.Context, start, end time.Time) (n int64, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		return tx.QueryRow(ctx, `SELECT COUNT(DISTINCT host_key) FROM host_metrics WHERE first_seen BETWEEN $1 AND $2 AND first_seen > 0;`, sqlTime(start), sqlTime(end)).Scan(&n)
	})
	return
}

// NewRenters returns the count of distinct renters whose first recorded event
// (first_seen) falls in [start, end].
func (s *Store) NewRenters(ctx context.Context, start, end time.Time) (n int64, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		return tx.QueryRow(ctx, `SELECT COUNT(DISTINCT renter_key) FROM renter_metrics WHERE first_seen BETWEEN $1 AND $2 AND first_seen > 0;`, sqlTime(start), sqlTime(end)).Scan(&n)
	})
	return
}

// TopHosts retrieves the top hosts based on earned revenue within a specified time range.
// The results are limited to the specified number of hosts.
func (s *Store) TopHosts(ctx context.Context, start, end time.Time, limit int) (ms []metrics.Host, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		rows, err := tx.Query(ctx, `SELECT host_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, revision_count, active_size, total_size, bytes_uploaded, burnt_collateral, locked_collateral, risked_collateral, potential_revenue, earned_revenue, first_seen, last_active, date_created
FROM host_metrics WHERE date_created BETWEEN $1 AND $2 GROUP BY host_key ORDER BY earned_revenue DESC LIMIT $3;`,
			sqlTime(start), sqlTime(end), limit)
		if err != nil {
			return fmt.Errorf("failed to query top hosts: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			m, err := scanHost(rows)
			if err != nil {
				return fmt.Errorf("failed to scan host metrics: %w", err)
			}
			ms = append(ms, m)
		}
		return rows.Err()
	})
	return
}

// TopRenters retrieves the top renters based on spent allowance within a specified time range.
// The results are limited to the specified number of renters.
func (s *Store) TopRenters(ctx context.Context, start, end time.Time, limit int) (ms []metrics.Renter, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		rows, err := tx.Query(ctx, `SELECT renter_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, revision_count, active_size, total_size, bytes_uploaded, locked_allowance, max(spent_allowance), tax, first_seen, last_active, date_created
FROM renter_metrics WHERE date_created BETWEEN $1 AND $2 GROUP BY renter_key ORDER BY spent_allowance DESC LIMIT $3;`,
			sqlTime(start), sqlTime(end), limit)
		if err != nil {
			return fmt.Errorf("failed to query top renters: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			m, err := scanRenter(rows)
			if err != nil {
				return fmt.Errorf("failed to scan renter metrics: %w", err)
			}
			ms = append(ms, m)
		}
		return rows.Err()
	})
	return
}

func getRenterMetrics(ctx context.Context, tx *txn, renterKey types.PublicKey, timestamp time.Time) (metrics.Renter, error) {
	row := tx.QueryRow(ctx, `SELECT renter_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, revision_count,
	active_size, total_size, bytes_uploaded, locked_allowance, spent_allowance, tax, first_seen, last_active, date_created
FROM renter_metrics WHERE renter_key=$1 AND date_created <= $2
ORDER BY date_created DESC LIMIT 1;`, sqlHash256(renterKey), sqlTime(timestamp))
	m, err := scanRenter(row)
	m.PublicKey = renterKey
	m.Timestamp = timestamp
	return m, err
}

func getHostMetrics(ctx context.Context, tx *txn, hostKey types.PublicKey, timestamp time.Time) (metrics.Host, error) {
	row := tx.QueryRow(ctx, `SELECT host_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, revision_count,
	active_size, total_size, bytes_uploaded, burnt_collateral, locked_collateral, risked_collateral, potential_revenue, earned_revenue, first_seen, last_active, date_created
FROM host_metrics WHERE host_key=$1 AND date_created <= $2
ORDER BY date_created DESC LIMIT 1;`, sqlHash256(hostKey), sqlTime(timestamp))
	m, err := scanHost(row)
	m.PublicKey = hostKey
	m.Timestamp = timestamp
	return m, err
}

func getMetrics(ctx context.Context, tx *txn, timestamp time.Time) (metrics.Metrics, error) {
	row := tx.QueryRow(ctx, `SELECT renters, hosts, active_contracts, renewed_contracts, successful_contracts, failed_contracts, v2_transaction_count, revision_count,
active_size, total_size, bytes_uploaded, active_byte_days, spent_allowance, locked_allowance, tax, potential_revenue, earned_revenue, locked_collateral,
risked_collateral, burnt_collateral, date_created
FROM metrics WHERE date_created <= $1
ORDER BY date_created DESC LIMIT 1;`, sqlTime(timestamp))
	m, err := scanMetrics(row)
	m.Timestamp = timestamp
	return m, err
}

func insertHostMetrics(ctx context.Context, tx *txn, m metrics.Host) error {
	// UPSERT on the (host_key, date_created) primary key so an in-place
	// update is performed when a row already exists for the snapshot hour,
	// instead of INSERT OR REPLACE's delete-then-insert (which fires CHECK
	// constraints from scratch and would invalidate any future foreign-key
	// references to the row).
	_, err := tx.Exec(ctx, `INSERT INTO host_metrics (host_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, revision_count, active_size, total_size, bytes_uploaded, burnt_collateral, locked_collateral, risked_collateral, potential_revenue, earned_revenue, first_seen, last_active, date_created)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
ON CONFLICT(host_key, date_created) DO UPDATE SET
	active_contracts = excluded.active_contracts,
	renewed_contracts = excluded.renewed_contracts,
	successful_contracts = excluded.successful_contracts,
	failed_contracts = excluded.failed_contracts,
	revision_count = excluded.revision_count,
	active_size = excluded.active_size,
	total_size = excluded.total_size,
	bytes_uploaded = excluded.bytes_uploaded,
	burnt_collateral = excluded.burnt_collateral,
	locked_collateral = excluded.locked_collateral,
	risked_collateral = excluded.risked_collateral,
	potential_revenue = excluded.potential_revenue,
	earned_revenue = excluded.earned_revenue,
	first_seen = excluded.first_seen,
	last_active = excluded.last_active;`,
		sqlHash256(m.PublicKey),
		m.ActiveContracts,
		m.RenewedContracts,
		m.SuccessfulContracts,
		m.FailedContracts,
		m.RevisionCount,
		m.ActiveSize,
		m.TotalSize,
		m.BytesUploaded,
		sqlCurrency(m.BurntCollateral),
		sqlCurrency(m.LockedCollateral),
		sqlCurrency(m.RiskedCollateral),
		sqlCurrency(m.PotentialRevenue),
		sqlCurrency(m.EarnedRevenue),
		sqlTime(m.FirstSeen),
		sqlTime(m.LastActive),
		sqlTime(m.Timestamp))
	return err
}

func insertRenterMetrics(ctx context.Context, tx *txn, m metrics.Renter) error {
	_, err := tx.Exec(ctx, `INSERT INTO renter_metrics (renter_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, revision_count, active_size, total_size, bytes_uploaded, locked_allowance, spent_allowance, tax, first_seen, last_active, date_created)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT(renter_key, date_created) DO UPDATE SET
	active_contracts = excluded.active_contracts,
	renewed_contracts = excluded.renewed_contracts,
	successful_contracts = excluded.successful_contracts,
	failed_contracts = excluded.failed_contracts,
	revision_count = excluded.revision_count,
	active_size = excluded.active_size,
	total_size = excluded.total_size,
	bytes_uploaded = excluded.bytes_uploaded,
	locked_allowance = excluded.locked_allowance,
	spent_allowance = excluded.spent_allowance,
	tax = excluded.tax,
	first_seen = excluded.first_seen,
	last_active = excluded.last_active;`,
		sqlHash256(m.PublicKey),
		m.ActiveContracts,
		m.RenewedContracts,
		m.SuccessfulContracts,
		m.FailedContracts,
		m.RevisionCount,
		m.ActiveSize,
		m.TotalSize,
		m.BytesUploaded,
		sqlCurrency(m.Locked),
		sqlCurrency(m.Spent),
		sqlCurrency(m.Tax),
		sqlTime(m.FirstSeen),
		sqlTime(m.LastActive),
		sqlTime(m.Timestamp))
	return err
}

func insertMetrics(ctx context.Context, tx *txn, m metrics.Metrics) error {
	_, err := tx.Exec(ctx, `INSERT INTO metrics (renters, hosts, active_contracts,
renewed_contracts, successful_contracts, failed_contracts, v2_transaction_count, revision_count, active_size, total_size, bytes_uploaded, active_byte_days, spent_allowance, locked_allowance, tax,
potential_revenue, earned_revenue, locked_collateral, risked_collateral, burnt_collateral, date_created)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
ON CONFLICT(date_created) DO UPDATE SET
	renters = excluded.renters,
	hosts = excluded.hosts,
	active_contracts = excluded.active_contracts,
	renewed_contracts = excluded.renewed_contracts,
	successful_contracts = excluded.successful_contracts,
	failed_contracts = excluded.failed_contracts,
	v2_transaction_count = excluded.v2_transaction_count,
	revision_count = excluded.revision_count,
	active_size = excluded.active_size,
	total_size = excluded.total_size,
	bytes_uploaded = excluded.bytes_uploaded,
	active_byte_days = excluded.active_byte_days,
	spent_allowance = excluded.spent_allowance,
	locked_allowance = excluded.locked_allowance,
	tax = excluded.tax,
	potential_revenue = excluded.potential_revenue,
	earned_revenue = excluded.earned_revenue,
	locked_collateral = excluded.locked_collateral,
	risked_collateral = excluded.risked_collateral,
	burnt_collateral = excluded.burnt_collateral;`,
		m.Renters,
		m.Hosts,
		m.ActiveContracts,
		m.RenewedContracts,
		m.SuccessfulContracts,
		m.FailedContracts,
		m.TransactionCount,
		m.RevisionCount,
		m.ActiveSize,
		m.TotalSize,
		m.BytesUploaded,
		m.ActiveByteDays,
		sqlCurrency(m.SpentAllowance),
		sqlCurrency(m.LockedAllowance),
		sqlCurrency(m.Tax),
		sqlCurrency(m.PotentialRevenue),
		sqlCurrency(m.EarnedRevenue),
		sqlCurrency(m.LockedCollateral),
		sqlCurrency(m.RiskedCollateral),
		sqlCurrency(m.BurntCollateral),
		sqlTime(m.Timestamp))
	return err
}

func scanMetrics(s scanner) (m metrics.Metrics, err error) {
	err = s.Scan(
		&m.Renters,
		&m.Hosts,
		&m.ActiveContracts,
		&m.RenewedContracts,
		&m.SuccessfulContracts,
		&m.FailedContracts,
		&m.TransactionCount,
		&m.RevisionCount,
		&m.ActiveSize,
		&m.TotalSize,
		&m.BytesUploaded,
		&m.ActiveByteDays,
		(*sqlCurrency)(&m.SpentAllowance),
		(*sqlCurrency)(&m.LockedAllowance),
		(*sqlCurrency)(&m.Tax),
		(*sqlCurrency)(&m.PotentialRevenue),
		(*sqlCurrency)(&m.EarnedRevenue),
		(*sqlCurrency)(&m.LockedCollateral),
		(*sqlCurrency)(&m.RiskedCollateral),
		(*sqlCurrency)(&m.BurntCollateral),
		(*sqlTime)(&m.Timestamp),
	)
	return
}

func scanRenter(s scanner) (r metrics.Renter, err error) {
	err = s.Scan(
		(*sqlHash256)(&r.PublicKey),
		&r.ActiveContracts,
		&r.RenewedContracts,
		&r.SuccessfulContracts,
		&r.FailedContracts,
		&r.RevisionCount,
		&r.ActiveSize,
		&r.TotalSize,
		&r.BytesUploaded,
		(*sqlCurrency)(&r.Locked),
		(*sqlCurrency)(&r.Spent),
		(*sqlCurrency)(&r.Tax),
		(*sqlTime)(&r.FirstSeen),
		(*sqlTime)(&r.LastActive),
		(*sqlTime)(&r.Timestamp),
	)
	return
}

func scanHost(s scanner) (h metrics.Host, err error) {
	err = s.Scan(
		(*sqlHash256)(&h.PublicKey),
		&h.ActiveContracts,
		&h.RenewedContracts,
		&h.SuccessfulContracts,
		&h.FailedContracts,
		&h.RevisionCount,
		&h.ActiveSize,
		&h.TotalSize,
		&h.BytesUploaded,
		(*sqlCurrency)(&h.BurntCollateral),
		(*sqlCurrency)(&h.LockedCollateral),
		(*sqlCurrency)(&h.RiskedCollateral),
		(*sqlCurrency)(&h.PotentialRevenue),
		(*sqlCurrency)(&h.EarnedRevenue),
		(*sqlTime)(&h.FirstSeen),
		(*sqlTime)(&h.LastActive),
		(*sqlTime)(&h.Timestamp),
	)
	return
}
