package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.sia.tech/core/types"
	"go.sia.tech/metrics/metrics"
)

func testCurrency(v uint64) types.Currency { return types.NewCurrency64(v) }

func testPublicKey(v byte) types.PublicKey {
	var pk types.PublicKey
	pk[0] = v
	return pk
}

func testIndex(v byte) types.ChainIndex {
	return types.ChainIndex{
		Height: uint64(v),
		ID:     types.BlockID{v},
	}
}

func TestApplyRevertRefreshRevenueAccounting(t *testing.T) {
	ctx := context.Background()
	store, err := OpenDatabase(filepath.Join(t.TempDir(), "metrics.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ts0 := time.Unix(0, 0).UTC()
	hostKey, renterKey := testPublicKey(1), testPublicKey(2)
	oldFormation := metrics.State{
		Timestamp: ts0,
		Formations: []metrics.ContractFormation{{
			Host:                 hostKey,
			Renter:               renterKey,
			Size:                 64,
			BytesUploaded:        64,
			RenterAllowance:      testCurrency(50),
			RenterContractPrice:  testCurrency(30),
			HostLockedCollateral: testCurrency(100),
			HostRiskedCollateral: testCurrency(40),
			HostPotentialRevenue: testCurrency(30),
			RenterTax:            testCurrency(7),
		}},
	}
	if err := store.ApplyState(ctx, testIndex(1), oldFormation); err != nil {
		t.Fatal(err)
	}

	refresh := metrics.State{
		Timestamp: ts0.Add(time.Hour),
		Formations: []metrics.ContractFormation{{
			Host:                 hostKey,
			Renter:               renterKey,
			Size:                 64,
			RenterAllowance:      testCurrency(100),
			RenterContractPrice:  testCurrency(7),
			HostLockedCollateral: testCurrency(240),
			HostRiskedCollateral: testCurrency(40),
			HostPotentialRevenue: testCurrency(37),
			RenterTax:            testCurrency(13),
		}},
		Resolutions: []metrics.ContractResolution{{
			Host:                 hostKey,
			Renter:               renterKey,
			Size:                 64,
			Type:                 metrics.ResolutionTypeRenewed,
			RenterAllowance:      testCurrency(50),
			HostLockedCollateral: testCurrency(100),
			HostRiskedCollateral: testCurrency(40),
			HostPotentialRevenue: testCurrency(30),
			HostEarnedRevenue:    types.ZeroCurrency,
		}},
	}
	if err := store.ApplyState(ctx, testIndex(2), refresh); err != nil {
		t.Fatal(err)
	}
	global, err := store.GlobalMetric(ctx, refresh.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	assertStoreCurrency(t, "spent after refresh", global.SpentAllowance, testCurrency(37))
	assertStoreCurrency(t, "earned after refresh", global.EarnedRevenue, types.ZeroCurrency)
	assertStoreCurrency(t, "potential after refresh", global.PotentialRevenue, testCurrency(37))
	assertStoreCurrency(t, "locked after refresh", global.LockedAllowance, testCurrency(100))
	if global.ActiveContracts != 1 || global.RenewedContracts != 1 {
		t.Fatalf("unexpected contract counts: active=%d renewed=%d", global.ActiveContracts, global.RenewedContracts)
	}

	if err := store.RevertState(ctx, testIndex(1), refresh); err != nil {
		t.Fatal(err)
	}
	global, err = store.GlobalMetric(ctx, refresh.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	assertStoreCurrency(t, "spent after revert", global.SpentAllowance, testCurrency(30))
	assertStoreCurrency(t, "earned after revert", global.EarnedRevenue, types.ZeroCurrency)
	assertStoreCurrency(t, "potential after revert", global.PotentialRevenue, testCurrency(30))
	assertStoreCurrency(t, "locked after revert", global.LockedAllowance, testCurrency(50))
	if global.ActiveContracts != 1 || global.RenewedContracts != 0 {
		t.Fatalf("unexpected reverted contract counts: active=%d renewed=%d", global.ActiveContracts, global.RenewedContracts)
	}
}

func assertStoreCurrency(t *testing.T, name string, got, expected types.Currency) {
	t.Helper()
	if got != expected {
		t.Fatalf("%s: expected %v, got %v", name, expected, got)
	}
}

func assertUint64(t *testing.T, name string, got, expected uint64) {
	t.Helper()
	if got != expected {
		t.Fatalf("%s: expected %d, got %d", name, expected, got)
	}
}

// TestActiveSizeAndBytesUploadedAcrossLifecycle exercises the size-tracking
// invariants the global, host, and renter metrics must satisfy across a full
// contract lifecycle including a renewal:
//
//   - ActiveSize at every level returns to zero once all contracts resolve.
//   - BytesUploaded does NOT inflate on renewals (the new contract carries
//     the parent's bytes forward; no new upload happened).
//   - Revert is symmetric with Apply for both counters.
func TestActiveSizeAndBytesUploadedAcrossLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := OpenDatabase(filepath.Join(t.TempDir(), "metrics.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ts0 := time.Unix(0, 0).UTC()
	hostKey, renterKey := testPublicKey(1), testPublicKey(2)
	const size = uint64(1 << 20)

	// Block 1: fresh formation.
	formationState := metrics.State{
		Timestamp: ts0,
		Formations: []metrics.ContractFormation{{
			Host:                 hostKey,
			Renter:               renterKey,
			Size:                 size,
			BytesUploaded:        size, // fresh formation credits full Filesize
			RenterAllowance:      testCurrency(50),
			RenterContractPrice:  testCurrency(30),
			HostLockedCollateral: testCurrency(100),
			HostRiskedCollateral: testCurrency(40),
			HostPotentialRevenue: testCurrency(30),
			RenterTax:            testCurrency(7),
		}},
	}
	if err := store.ApplyState(ctx, testIndex(1), formationState); err != nil {
		t.Fatal(err)
	}

	// Block 2: a refresh — the old contract is renewed, the new contract
	// carries the same `size` bytes forward. BytesUploaded must not grow.
	refreshState := metrics.State{
		Timestamp: ts0.Add(time.Hour),
		Formations: []metrics.ContractFormation{{
			Host:                 hostKey,
			Renter:               renterKey,
			Size:                 size,
			BytesUploaded:        0, // renewal-created with no growth
			RenterAllowance:      testCurrency(100),
			RenterContractPrice:  testCurrency(7),
			HostLockedCollateral: testCurrency(240),
			HostRiskedCollateral: testCurrency(40),
			HostPotentialRevenue: testCurrency(37),
			RenterTax:            testCurrency(13),
		}},
		Resolutions: []metrics.ContractResolution{{
			Host:                 hostKey,
			Renter:               renterKey,
			Size:                 size,
			Type:                 metrics.ResolutionTypeRenewed,
			RenterAllowance:      testCurrency(50),
			HostLockedCollateral: testCurrency(100),
			HostRiskedCollateral: testCurrency(40),
			HostPotentialRevenue: testCurrency(30),
			HostEarnedRevenue:    types.ZeroCurrency,
		}},
	}
	if err := store.ApplyState(ctx, testIndex(2), refreshState); err != nil {
		t.Fatal(err)
	}

	// After the refresh: one active contract still holding `size`, and only
	// the original `size` ever counted as uploaded.
	global, err := store.GlobalMetric(ctx, refreshState.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	assertUint64(t, "global ActiveSize after refresh", global.ActiveSize, size)
	assertUint64(t, "global BytesUploaded after refresh", global.BytesUploaded, size)
	assertUint64(t, "global ActiveContracts after refresh", global.ActiveContracts, 1)

	host, err := store.HostMetric(ctx, hostKey, refreshState.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	assertUint64(t, "host ActiveSize after refresh", host.ActiveSize, size)
	assertUint64(t, "host BytesUploaded after refresh", host.BytesUploaded, size)

	renter, err := store.RenterMetric(ctx, renterKey, refreshState.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	assertUint64(t, "renter ActiveSize after refresh", renter.ActiveSize, size)
	assertUint64(t, "renter BytesUploaded after refresh", renter.BytesUploaded, size)

	// Block 3: the refreshed contract expires successfully. ActiveSize must
	// fall back to zero at every level; BytesUploaded does not change.
	expirationState := metrics.State{
		Timestamp: ts0.Add(2 * time.Hour),
		Resolutions: []metrics.ContractResolution{{
			Host:                 hostKey,
			Renter:               renterKey,
			Size:                 size,
			Type:                 metrics.ResolutionTypeExpired,
			RenterAllowance:      testCurrency(100),
			HostLockedCollateral: testCurrency(240),
			HostRiskedCollateral: testCurrency(40),
			HostPotentialRevenue: testCurrency(37),
			HostBurn:             types.ZeroCurrency,
		}},
	}
	if err := store.ApplyState(ctx, testIndex(3), expirationState); err != nil {
		t.Fatal(err)
	}

	global, err = store.GlobalMetric(ctx, expirationState.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	assertUint64(t, "global ActiveSize after expiration", global.ActiveSize, 0)
	assertUint64(t, "global BytesUploaded after expiration", global.BytesUploaded, size)
	assertUint64(t, "global ActiveContracts after expiration", global.ActiveContracts, 0)

	host, err = store.HostMetric(ctx, hostKey, expirationState.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	assertUint64(t, "host ActiveSize after expiration", host.ActiveSize, 0)
	assertUint64(t, "host BytesUploaded after expiration", host.BytesUploaded, size)

	renter, err = store.RenterMetric(ctx, renterKey, expirationState.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	assertUint64(t, "renter ActiveSize after expiration", renter.ActiveSize, 0)
	assertUint64(t, "renter BytesUploaded after expiration", renter.BytesUploaded, size)

	// Revert symmetry: reverting the expiration restores ActiveSize=size,
	// and reverting the refresh leaves the original formation untouched.
	if err := store.RevertState(ctx, testIndex(2), expirationState); err != nil {
		t.Fatal(err)
	}
	global, err = store.GlobalMetric(ctx, expirationState.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	assertUint64(t, "global ActiveSize after reverting expiration", global.ActiveSize, size)
	assertUint64(t, "global BytesUploaded after reverting expiration", global.BytesUploaded, size)

	if err := store.RevertState(ctx, testIndex(1), refreshState); err != nil {
		t.Fatal(err)
	}
	global, err = store.GlobalMetric(ctx, refreshState.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	assertUint64(t, "global ActiveSize after reverting refresh", global.ActiveSize, size)
	assertUint64(t, "global BytesUploaded after reverting refresh", global.BytesUploaded, size)
	assertUint64(t, "global ActiveContracts after reverting refresh", global.ActiveContracts, 1)
}
