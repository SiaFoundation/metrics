package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.sia.tech/core/types"
	"go.sia.tech/metrics/metrics"
	"lukechampine.com/frand"
)

func TestRenterMetrics(t *testing.T) {
	store, err := OpenDatabase(filepath.Join(t.TempDir(), "metrics.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	timestamp := time.Now().Truncate(time.Hour)
	rm := metrics.Renter{
		PublicKey:       frand.Entropy256(),
		ActiveContracts: 1,
		Locked:          types.Siacoins(100),
		Timestamp:       timestamp.Add(-2 * time.Hour),
	}

	err = store.transaction(t.Context(), func(ctx context.Context, tx *txn) error {
		return insertRenterMetrics(ctx, tx, rm)
	})
	if err != nil {
		t.Fatal(err)
	}

	assertMetrics := func(t *testing.T, timestamp time.Time, expected metrics.Renter) {
		t.Helper()

		var retrieved metrics.Renter
		err := store.transaction(t.Context(), func(ctx context.Context, tx *txn) (err error) {
			retrieved, err = getRenterMetrics(ctx, tx, rm.PublicKey, timestamp)
			return err
		})
		if err != nil {
			t.Fatal(err)
		} else if retrieved != expected {
			t.Errorf("expected %v, got %v", expected, retrieved)
		}
	}

	assertMetrics(t, rm.Timestamp, rm)
	assertMetrics(t, time.Now(), rm)

	// update the metrics with the same timestamp
	rm.ActiveContracts = 0
	rm.Locked = types.Siacoins(200)

	err = store.transaction(t.Context(), func(ctx context.Context, tx *txn) error {
		return insertRenterMetrics(ctx, tx, rm)
	})
	if err != nil {
		t.Fatal(err)
	}

	assertMetrics(t, rm.Timestamp, rm)
	assertMetrics(t, time.Now(), rm)

	// update the metrics
	updated := rm
	updated.ActiveContracts = 2
	updated.Locked = types.Siacoins(50)
	updated.Timestamp = timestamp.Add(-time.Hour)

	err = store.transaction(t.Context(), func(ctx context.Context, tx *txn) error {
		return insertRenterMetrics(ctx, tx, updated)
	})
	if err != nil {
		t.Fatal(err)
	}

	assertMetrics(t, rm.Timestamp, rm) // should still return the original metrics
	assertMetrics(t, time.Now().Add(-time.Minute), updated)
	assertMetrics(t, time.Now(), updated)
}

func TestHostMetrics(t *testing.T) {
	store, err := OpenDatabase(filepath.Join(t.TempDir(), "metrics.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	timestamp := time.Now().Truncate(time.Hour)
	rm := metrics.Renter{
		PublicKey:       frand.Entropy256(),
		ActiveContracts: 1,
		Locked:          types.Siacoins(100),
		Timestamp:       timestamp.Add(-2 * time.Hour),
	}

	err = store.transaction(t.Context(), func(ctx context.Context, tx *txn) error {
		return insertRenterMetrics(ctx, tx, rm)
	})
	if err != nil {
		t.Fatal(err)
	}

	assertMetrics := func(t *testing.T, timestamp time.Time, expected metrics.Renter) {
		t.Helper()

		var retrieved metrics.Renter
		err := store.transaction(t.Context(), func(ctx context.Context, tx *txn) (err error) {
			retrieved, err = getRenterMetrics(ctx, tx, rm.PublicKey, timestamp)
			return err
		})
		if err != nil {
			t.Fatal(err)
		} else if retrieved != expected {
			t.Errorf("expected %v, got %v", expected, retrieved)
		}
	}

	assertMetrics(t, rm.Timestamp, rm)
	assertMetrics(t, time.Now(), rm)

	// update the metrics with the same timestamp
	rm.ActiveContracts = 0
	rm.Locked = types.Siacoins(200)

	err = store.transaction(t.Context(), func(ctx context.Context, tx *txn) error {
		return insertRenterMetrics(ctx, tx, rm)
	})
	if err != nil {
		t.Fatal(err)
	}

	assertMetrics(t, rm.Timestamp, rm)
	assertMetrics(t, time.Now(), rm)

	// update the metrics
	updated := rm
	updated.ActiveContracts = 2
	updated.Locked = types.Siacoins(50)
	updated.Timestamp = timestamp.Add(-time.Hour)

	err = store.transaction(t.Context(), func(ctx context.Context, tx *txn) error {
		return insertRenterMetrics(ctx, tx, updated)
	})
	if err != nil {
		t.Fatal(err)
	}

	assertMetrics(t, rm.Timestamp, rm) // should still return the original metrics
	assertMetrics(t, time.Now().Add(-time.Minute), updated)
	assertMetrics(t, time.Now(), updated)
}
