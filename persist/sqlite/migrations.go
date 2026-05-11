package sqlite

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// migrateAddTaxColumns adds the `tax` column to the renter_metrics and metrics
// tables to track cumulative siafund tax paid on v2 contract formations and
// renewals. Existing rows are backfilled to zero, which is correct: tax was
// not previously tracked, and the column accumulates from this point forward
// (the manager re-indexes from genesis when last_index is invalidated, but
// historical aggregates are otherwise treated as zero for this metric).
func migrateAddTaxColumns(ctx context.Context, tx *txn, log *zap.Logger) error {
	const stmt = `
ALTER TABLE renter_metrics ADD COLUMN tax BLOB NOT NULL DEFAULT x'00000000000000000000000000000000' CHECK(length(tax) = 16);
ALTER TABLE metrics ADD COLUMN tax BLOB NOT NULL DEFAULT x'00000000000000000000000000000000' CHECK(length(tax) = 16);
`
	if _, err := tx.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("failed to add tax columns: %w", err)
	}
	log.Info("added tax columns to renter_metrics and metrics tables")
	return nil
}

// migrateAddActivityCounters adds transaction and revision counters used to
// surface daily on-chain activity:
//   - metrics.v2_transaction_count: cumulative count of v2 transactions across
//     all indexed blocks.
//   - metrics/host_metrics/renter_metrics.revision_count: cumulative count of
//     v2 contract revision events.
//
// All columns are backfilled to zero; the counters accumulate going forward.
func migrateAddActivityCounters(ctx context.Context, tx *txn, log *zap.Logger) error {
	const stmt = `
ALTER TABLE metrics ADD COLUMN v2_transaction_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE metrics ADD COLUMN revision_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE host_metrics ADD COLUMN revision_count INTEGER NOT NULL DEFAULT 0 CHECK (revision_count >= 0);
ALTER TABLE renter_metrics ADD COLUMN revision_count INTEGER NOT NULL DEFAULT 0 CHECK (revision_count >= 0);
`
	if _, err := tx.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("failed to add activity counter columns: %w", err)
	}
	log.Info("added activity counter columns")
	return nil
}

// migrateAddActivityTimestamps adds per-key activity timestamps and a global
// byte-days counter:
//   - host_metrics/renter_metrics.first_seen: first block in which a key had
//     any v2 contract event (used to derive "new in period" counts).
//   - host_metrics/renter_metrics.last_active: most recent event block.
//   - metrics.active_byte_days: cumulative byte-days of storage held by the
//     network.
//
// Existing rows are backfilled by deriving first_seen/last_active from the
// min/max date_created per key. active_byte_days starts at zero and
// accumulates from this point forward.
func migrateAddActivityTimestamps(ctx context.Context, tx *txn, log *zap.Logger) error {
	const stmt = `
ALTER TABLE host_metrics ADD COLUMN first_seen INTEGER NOT NULL DEFAULT 0;
ALTER TABLE host_metrics ADD COLUMN last_active INTEGER NOT NULL DEFAULT 0;
ALTER TABLE renter_metrics ADD COLUMN first_seen INTEGER NOT NULL DEFAULT 0;
ALTER TABLE renter_metrics ADD COLUMN last_active INTEGER NOT NULL DEFAULT 0;
ALTER TABLE metrics ADD COLUMN active_byte_days INTEGER NOT NULL DEFAULT 0;

UPDATE host_metrics SET
	first_seen = (SELECT MIN(date_created) FROM host_metrics h2 WHERE h2.host_key = host_metrics.host_key),
	last_active = (SELECT MAX(date_created) FROM host_metrics h2 WHERE h2.host_key = host_metrics.host_key);
UPDATE renter_metrics SET
	first_seen = (SELECT MIN(date_created) FROM renter_metrics r2 WHERE r2.renter_key = renter_metrics.renter_key),
	last_active = (SELECT MAX(date_created) FROM renter_metrics r2 WHERE r2.renter_key = renter_metrics.renter_key);
`
	if _, err := tx.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("failed to add activity timestamp columns: %w", err)
	}
	log.Info("added activity timestamp columns and backfilled first_seen/last_active")
	return nil
}

// migrateAddBytesUploaded adds a monotonically-increasing byte counter to
// host_metrics, renter_metrics, and metrics. Unlike total_size — which
// tracks chain-state contract size and can decrease on shrink revisions —
// bytes_uploaded only ever grows.
//
// Existing rows are backfilled to total_size as a best-effort lower bound:
// any net positive bytes are at least counted, but the migration cannot
// recover history of shrunk-then-resolved contracts. From the migration
// point forward, formations contribute +Size and grow revisions contribute
// +(NewSize - ExistingSize).
func migrateAddBytesUploaded(ctx context.Context, tx *txn, log *zap.Logger) error {
	const stmt = `
ALTER TABLE host_metrics ADD COLUMN bytes_uploaded INTEGER NOT NULL DEFAULT 0;
ALTER TABLE renter_metrics ADD COLUMN bytes_uploaded INTEGER NOT NULL DEFAULT 0;
ALTER TABLE metrics ADD COLUMN bytes_uploaded INTEGER NOT NULL DEFAULT 0;

UPDATE host_metrics SET bytes_uploaded = total_size;
UPDATE renter_metrics SET bytes_uploaded = total_size;
UPDATE metrics SET bytes_uploaded = total_size;
`
	if _, err := tx.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("failed to add bytes_uploaded columns: %w", err)
	}
	log.Info("added bytes_uploaded columns and backfilled from total_size")
	return nil
}

// migrations is a list of functions that are run to migrate the database from
// one version to the next. Migrations are used to update existing databases to
// match the schema in init.sql.
var migrations = []func(ctx context.Context, tx *txn, log *zap.Logger) error{
	migrateAddTaxColumns,
	migrateAddActivityCounters,
	migrateAddActivityTimestamps,
	migrateAddBytesUploaded,
}
