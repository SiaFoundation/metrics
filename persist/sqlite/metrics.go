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

func getRenterMetrics(ctx context.Context, tx *txn, renterKey types.PublicKey, timestamp time.Time) (metrics.Renter, error) {
	row := tx.QueryRow(ctx, `SELECT renter_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, 
	active_size, total_size, locked_allowance, spent_allowance, date_created 
FROM renter_metrics WHERE renter_key=$1 AND date_created <= $2
ORDER BY date_created DESC LIMIT 1;`, sqlHash256(renterKey), sqlTime(timestamp))
	m, err := scanRenter(row)
	m.PublicKey = renterKey
	m.Timestamp = timestamp
	return m, err
}

func getHostMetrics(ctx context.Context, tx *txn, hostKey types.PublicKey, timestamp time.Time) (metrics.Host, error) {
	row := tx.QueryRow(ctx, `SELECT host_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, 
	active_size, total_size, burnt_collateral, locked_collateral, risked_collateral, potential_revenue, earned_revenue, date_created 
FROM host_metrics WHERE host_key=$1 AND date_created <= $2
ORDER BY date_created DESC LIMIT 1;`, sqlHash256(hostKey), sqlTime(timestamp))
	m, err := scanHost(row)
	m.PublicKey = hostKey
	m.Timestamp = timestamp
	return m, err
}

func getMetrics(ctx context.Context, tx *txn, timestamp time.Time) (metrics.Metrics, error) {
	row := tx.QueryRow(ctx, `SELECT renters, hosts, active_contracts, renewed_contracts, successful_contracts, failed_contracts, 
active_size, total_size, spent_allowance, locked_allowance, potential_revenue, earned_revenue, locked_collateral, 
risked_collateral, burnt_collateral, date_created
FROM metrics WHERE date_created <= $1
ORDER BY date_created DESC LIMIT 1;`, sqlTime(timestamp))
	m, err := scanMetrics(row)
	m.Timestamp = timestamp
	return m, err
}

func insertHostMetrics(ctx context.Context, tx *txn, m metrics.Host) error {
	_, err := tx.Exec(ctx, `INSERT OR REPLACE INTO host_metrics (host_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, active_size, total_size, burnt_collateral, locked_collateral, risked_collateral, potential_revenue, earned_revenue, date_created)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);`,
		sqlHash256(m.PublicKey),
		m.ActiveContracts,
		m.RenewedContracts,
		m.SuccessfulContracts,
		m.FailedContracts,
		m.ActiveSize,
		m.TotalSize,
		sqlCurrency(m.BurntCollateral),
		sqlCurrency(m.LockedCollateral),
		sqlCurrency(m.RiskedCollateral),
		sqlCurrency(m.PotentialRevenue),
		sqlCurrency(m.EarnedRevenue),
		sqlTime(m.Timestamp))
	return err
}

func insertRenterMetrics(ctx context.Context, tx *txn, m metrics.Renter) error {
	_, err := tx.Exec(ctx, `INSERT OR REPLACE INTO renter_metrics (renter_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, active_size, total_size, locked_allowance, spent_allowance, date_created)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);`,
		sqlHash256(m.PublicKey),
		m.ActiveContracts,
		m.RenewedContracts,
		m.SuccessfulContracts,
		m.FailedContracts,
		m.ActiveSize,
		m.TotalSize,
		sqlCurrency(m.Locked),
		sqlCurrency(m.Spent),
		sqlTime(m.Timestamp))
	return err
}

func insertMetrics(ctx context.Context, tx *txn, m metrics.Metrics) error {
	_, err := tx.Exec(ctx, `INSERT OR REPLACE INTO metrics (renters, hosts, active_contracts, 
renewed_contracts, successful_contracts, failed_contracts, active_size, total_size, spent_allowance, locked_allowance,
potential_revenue, earned_revenue, locked_collateral, risked_collateral, burnt_collateral, date_created)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16);`,
		m.Renters,
		m.Hosts,
		m.ActiveContracts,
		m.RenewedContracts,
		m.SuccessfulContracts,
		m.FailedContracts,
		m.ActiveSize,
		m.TotalSize,
		sqlCurrency(m.SpentAllowance),
		sqlCurrency(m.LockedAllowance),
		sqlCurrency(m.PotentialRevenue),
		sqlCurrency(m.EarnedRevenue),
		sqlCurrency(m.LockedCollateral),
		sqlCurrency(m.RiskedCollateral),
		sqlCurrency(m.BurntCollateral),
		sqlTime(m.Timestamp))
	return err
}

func (s *Store) RevertState(ctx context.Context, tip types.ChainIndex, state metrics.State) error {
	return s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		for _, formation := range state.Formations {
			gm, err := getMetrics(ctx, tx, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get global metrics: %w", err)
			}
			gm.ActiveContracts--
			gm.LockedAllowance = gm.LockedAllowance.Sub(formation.RenterAllowance)
			gm.PotentialRevenue = gm.PotentialRevenue.Sub(formation.HostPotentialRevenue)
			gm.LockedCollateral = gm.LockedCollateral.Sub(formation.HostLockedCollateral)
			gm.RiskedCollateral = gm.RiskedCollateral.Sub(formation.HostRiskedCollateral)

			hm, err := getHostMetrics(ctx, tx, formation.Host, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get host metrics for %q: %w", formation.Host, err)
			}
			hm.ActiveContracts--
			hm.ActiveSize -= formation.Size
			hm.TotalSize -= formation.Size
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
			rm.Locked = rm.Locked.Sub(formation.RenterAllowance)

			if err := insertMetrics(ctx, tx, gm); err != nil {
				return fmt.Errorf("failed to insert global metrics: %w", err)
			} else if err := insertHostMetrics(ctx, tx, hm); err != nil {
				return fmt.Errorf("failed to insert host metrics for %q: %w", hm.PublicKey, err)
			} else if err := insertRenterMetrics(ctx, tx, rm); err != nil {
				return fmt.Errorf("failed to insert renter metrics for %q: %w", rm.PublicKey, err)
			}
		}

		for _, revision := range state.Revisions {
			hm, err := getHostMetrics(ctx, tx, revision.Host, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get host metrics for %q: %w", revision.Host, err)
			}
			hm.RiskedCollateral = hm.RiskedCollateral.Sub(revision.NewRiskedCollateral).Add(revision.ExistingRiskedCollateral)
			hm.PotentialRevenue = hm.PotentialRevenue.Sub(revision.NewPotentialRevenue).Add(revision.ExistingPotentialRevenue)
			hm.ActiveSize = hm.ActiveSize - revision.NewSize + revision.ExistingSize
			hm.TotalSize = hm.TotalSize - revision.NewSize + revision.ExistingSize

			rm, err := getRenterMetrics(ctx, tx, revision.Renter, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get renter metrics for %q: %w", revision.Renter, err)
			}
			rm.ActiveSize = rm.ActiveSize - revision.NewSize + revision.ExistingSize
			rm.TotalSize = rm.TotalSize - revision.NewSize + revision.ExistingSize
			rm.Locked = rm.Locked.Sub(revision.NewAllowance).Add(revision.ExistingAllowance)
			spent, ok := revision.NewAllowance.SubWithUnderflow(revision.ExistingAllowance)
			if ok {
				rm.Spent = rm.Spent.Sub(spent)
			}

			gm, err := getMetrics(ctx, tx, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get global metrics: %w", err)
			}
			gm.ActiveSize = gm.ActiveSize - revision.NewSize + revision.ExistingSize
			gm.TotalSize = gm.TotalSize - revision.NewSize + revision.ExistingSize
			gm.LockedAllowance = gm.LockedAllowance.Sub(revision.NewAllowance).Add(revision.ExistingAllowance)
			gm.PotentialRevenue = gm.PotentialRevenue.Sub(revision.NewPotentialRevenue).Add(revision.ExistingPotentialRevenue)
			gm.RiskedCollateral = gm.RiskedCollateral.Sub(revision.NewRiskedCollateral).Add(revision.ExistingRiskedCollateral)
			if ok {
				gm.SpentAllowance = gm.SpentAllowance.Sub(spent)
			}

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

			gm, err := getMetrics(ctx, tx, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get global metrics: %w", err)
			}
			gm.ActiveContracts++
			gm.EarnedRevenue = gm.EarnedRevenue.Sub(resolution.HostEarnedRevenue)
			gm.BurntCollateral = gm.BurntCollateral.Sub(resolution.HostBurn)
			gm.LockedAllowance = gm.LockedAllowance.Add(resolution.RenterAllowance)
			gm.PotentialRevenue = gm.PotentialRevenue.Add(resolution.HostPotentialRevenue)
			gm.LockedCollateral = gm.LockedCollateral.Add(resolution.HostLockedCollateral)
			gm.RiskedCollateral = gm.RiskedCollateral.Add(resolution.HostRiskedCollateral)

			switch resolution.Type {
			case metrics.ResolutionTypeProof:
				hm.SuccessfulContracts--
				rm.SuccessfulContracts--
				gm.SuccessfulContracts--
			case metrics.ResolutionTypeExpired:
				if hm.BurntCollateral.IsZero() {
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

		_, err := tx.Exec(ctx, `UPDATE global_settings SET last_index = $1;`, encodable(tip))
		return err
	})
}

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
			gm.LockedAllowance = gm.LockedAllowance.Add(formation.RenterAllowance)
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
			hm.LockedCollateral = hm.LockedCollateral.Add(formation.HostLockedCollateral)
			hm.RiskedCollateral = hm.RiskedCollateral.Add(formation.HostRiskedCollateral)
			hm.PotentialRevenue = hm.PotentialRevenue.Add(formation.HostPotentialRevenue)
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
			rm.Locked = rm.Locked.Add(formation.RenterAllowance)
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
			hm, err := getHostMetrics(ctx, tx, revision.Host, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get revision host metrics for %q: %w", revision.Host, err)
			}
			s.log.Debug("updating host metrics for revision", zap.Stringer("host", hm.PublicKey), zap.Stringer("totalRisked", hm.RiskedCollateral), zap.Stringer("newRisked", revision.NewRiskedCollateral), zap.Stringer("existingRisked", revision.ExistingRiskedCollateral), zap.Time("timestamp", state.Timestamp))
			hm.RiskedCollateral = hm.RiskedCollateral.Sub(revision.ExistingRiskedCollateral).Add(revision.NewRiskedCollateral)
			hm.PotentialRevenue = hm.PotentialRevenue.Sub(revision.ExistingPotentialRevenue).Add(revision.NewPotentialRevenue)
			hm.ActiveSize = hm.ActiveSize - revision.ExistingSize + revision.NewSize
			hm.TotalSize = hm.TotalSize - revision.ExistingSize + revision.NewSize
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
			rm.Locked = rm.Locked.Sub(revision.ExistingAllowance).Add(revision.NewAllowance)
			rm.Spent = rm.Spent.Add(spent)
			s.log.Debug("updating renter metrics for revision", zap.Stringer("renter", rm.PublicKey), zap.Stringer("spent", spent))

			gm, err := getMetrics(ctx, tx, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get global metrics: %w", err)
			}
			gm.ActiveSize = gm.ActiveSize - revision.ExistingSize + revision.NewSize
			gm.TotalSize = gm.TotalSize - revision.ExistingSize + revision.NewSize
			gm.LockedAllowance = gm.LockedAllowance.Sub(revision.ExistingAllowance).Add(revision.NewAllowance)
			gm.PotentialRevenue = gm.PotentialRevenue.Sub(revision.ExistingPotentialRevenue).Add(revision.NewPotentialRevenue)
			gm.RiskedCollateral = gm.RiskedCollateral.Sub(revision.ExistingRiskedCollateral).Add(revision.NewRiskedCollateral)
			gm.SpentAllowance = gm.SpentAllowance.Add(spent)

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

			rm, err := getRenterMetrics(ctx, tx, resolution.Renter, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get resolution renter metrics for %q: %w", resolution.Renter, err)
			}
			s.log.Debug("updating renter metrics for resolution", zap.Stringer("renter", rm.PublicKey), zap.Any("before", rm))
			rm.ActiveContracts--
			rm.ActiveSize -= resolution.Size
			rm.Locked = rm.Locked.Sub(resolution.RenterAllowance)
			rm.Spent = rm.Spent.Add(resolution.RenterSpent)
			s.log.Debug("updating renter metrics for resolution", zap.Stringer("renter", rm.PublicKey), zap.Any("after", rm))

			gm, err := getMetrics(ctx, tx, state.Timestamp)
			if err != nil {
				return fmt.Errorf("failed to get global metrics: %w", err)
			}
			gm.ActiveContracts--
			gm.SpentAllowance = gm.SpentAllowance.Add(resolution.RenterSpent)
			gm.LockedAllowance = gm.LockedAllowance.Sub(resolution.RenterAllowance)
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
				if hm.BurntCollateral.IsZero() {
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

		_, err := tx.Exec(ctx, `UPDATE global_settings SET last_index = $1;`, encodable(tip))
		return err
	})
}

func (s *Store) RenterMetric(ctx context.Context, renterKey types.PublicKey, timestamp time.Time) (m metrics.Renter, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		row := tx.QueryRow(ctx, `SELECT renter_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, active_size, total_size, locked_allowance, spent_allowance, date_created
FROM renter_metrics WHERE renter_key=$1 AND date_created <= $2 ORDER BY date_created DESC LIMIT 1;`, sqlHash256(renterKey), sqlTime(timestamp))

		m, err = scanRenter(row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // no metrics found for this renter
		}
		return err
	})
	return
}

func (s *Store) HostMetric(ctx context.Context, hostKey types.PublicKey, timestamp time.Time) (m metrics.Host, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		row := tx.QueryRow(ctx, `SELECT host_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, active_size, total_size, burnt_collateral, locked_collateral, risked_collateral, potential_revenue, earned_revenue, date_created
FROM host_metrics WHERE host_key=$1 AND date_created <= $2 ORDER BY date_created DESC LIMIT 1;`, sqlHash256(hostKey), sqlTime(timestamp))
		m, err = scanHost(row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // no metrics found for this host
		}
		return err
	})
	return
}

func (s *Store) GlobalMetric(ctx context.Context, timestamp time.Time) (m metrics.Metrics, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		row := tx.QueryRow(ctx, `SELECT renters, hosts, active_contracts, renewed_contracts, successful_contracts, failed_contracts, active_size, total_size, spent_allowance, locked_allowance,
potential_revenue, earned_revenue, locked_collateral, risked_collateral, burnt_collateral, date_created
FROM metrics WHERE date_created <= $1 ORDER BY date_created DESC LIMIT 1;`, sqlTime(timestamp))
		m, err = scanMetrics(row)
		return err
	})
	return
}

func (s *Store) HostMetrics(ctx context.Context, hostKey types.PublicKey, start, end time.Time) (ms []metrics.Host, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		rows, err := tx.Query(ctx, `SELECT host_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, active_size, total_size, burnt_collateral, locked_collateral, risked_collateral, potential_revenue, earned_revenue, date_created
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

func (s *Store) RenterMetrics(ctx context.Context, renterKey types.PublicKey, start, end time.Time) (ms []metrics.Renter, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		rows, err := tx.Query(ctx, `SELECT renter_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, active_size, total_size, locked_allowance, spent_allowance, date_created
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

func (s *Store) GlobalMetrics(ctx context.Context, start, end time.Time) (ms []metrics.Metrics, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		rows, err := tx.Query(ctx, `SELECT renters, hosts, active_contracts, renewed_contracts, successful_contracts, failed_contracts, active_size, total_size, spent_allowance, locked_allowance,
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

func (s *Store) TopHosts(ctx context.Context, start, end time.Time, limit int) (ms []metrics.Host, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		rows, err := tx.Query(ctx, `SELECT host_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, active_size, total_size, burnt_collateral, locked_collateral, risked_collateral, potential_revenue, earned_revenue, date_created
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

func (s *Store) TopRenters(ctx context.Context, start, end time.Time, limit int) (ms []metrics.Renter, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		rows, err := tx.Query(ctx, `SELECT renter_key, active_contracts, renewed_contracts, successful_contracts, failed_contracts, active_size, total_size, locked_allowance, max(spent_allowance), date_created
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

func scanMetrics(s scanner) (m metrics.Metrics, err error) {
	err = s.Scan(
		&m.Renters,
		&m.Hosts,
		&m.ActiveContracts,
		&m.RenewedContracts,
		&m.SuccessfulContracts,
		&m.FailedContracts,
		&m.ActiveSize,
		&m.TotalSize,
		(*sqlCurrency)(&m.SpentAllowance),
		(*sqlCurrency)(&m.LockedAllowance),
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
		&r.ActiveSize,
		&r.TotalSize,
		(*sqlCurrency)(&r.Locked),
		(*sqlCurrency)(&r.Spent),
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
		&h.ActiveSize,
		&h.TotalSize,
		(*sqlCurrency)(&h.BurntCollateral),
		(*sqlCurrency)(&h.LockedCollateral),
		(*sqlCurrency)(&h.RiskedCollateral),
		(*sqlCurrency)(&h.PotentialRevenue),
		(*sqlCurrency)(&h.EarnedRevenue),
		(*sqlTime)(&h.Timestamp),
	)
	return
}
