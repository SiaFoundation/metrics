package metrics

import (
	"testing"
	"time"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.uber.org/zap"
)

func testCurrency(v uint64) types.Currency { return types.NewCurrency64(v) }

func testPublicKey(v byte) types.PublicKey {
	var pk types.PublicKey
	pk[0] = v
	return pk
}

func testFileContract(proofHeight, expirationHeight, filesize uint64, renterAllowance, totalCollateral, missedHostValue, hostRevenue types.Currency) types.V2FileContract {
	return types.V2FileContract{
		Capacity:         filesize,
		Filesize:         filesize,
		ProofHeight:      proofHeight,
		ExpirationHeight: expirationHeight,
		RenterOutput: types.SiacoinOutput{
			Value: renterAllowance,
		},
		HostOutput: types.SiacoinOutput{
			Value: totalCollateral.Add(hostRevenue),
		},
		MissedHostValue: missedHostValue,
		TotalCollateral: totalCollateral,
		RenterPublicKey: testPublicKey(1),
		HostPublicKey:   testPublicKey(2),
	}
}

func testContractID(v byte) types.FileContractID {
	var id types.FileContractID
	id[0] = v
	return id
}

func testParseDiffs(t *testing.T, txns []types.V2Transaction) State {
	t.Helper()
	state, err := parseDiffs(time.Unix(0, 0), consensus.State{}, txns, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertCurrency(t *testing.T, name string, got, expected types.Currency) {
	t.Helper()
	if got != expected {
		t.Fatalf("%s: expected %v, got %v", name, expected, got)
	}
}

func TestParseDiffsFreshFormationSpending(t *testing.T) {
	fc := testFileContract(100, 110, 64, testCurrency(100), testCurrency(200), testCurrency(200), testCurrency(7))
	state := testParseDiffs(t, []types.V2Transaction{{
		FileContracts: []types.V2FileContract{fc},
	}})

	if len(state.Formations) != 1 {
		t.Fatalf("expected one formation, got %d", len(state.Formations))
	}
	assertCurrency(t, "formation spend", state.Formations[0].RenterContractPrice, testCurrency(7))
	assertCurrency(t, "formation tax", state.Formations[0].RenterTax, consensus.State{}.V2FileContractTax(fc))
}

// assertRenewalSumConserved checks the consensus rule that
// FinalRenter + FinalHost + RenterRollover + HostRollover == parent.RenterOutput.Value + parent.HostOutput.Value.
// All fixtures in this file model real renewals that consensus would accept.
func assertRenewalSumConserved(t *testing.T, parent types.V2FileContract, r *types.V2FileContractRenewal) {
	t.Helper()
	got := r.FinalRenterOutput.Value.Add(r.RenterRollover).Add(r.FinalHostOutput.Value).Add(r.HostRollover)
	want := parent.RenterOutput.Value.Add(parent.HostOutput.Value)
	if got != want {
		t.Fatalf("renewal sum %v != parent sum %v", got, want)
	}
}

// renewalTxn wraps a parent contract + renewal resolution in the
// V2Transaction shape parseDiffs consumes.
func renewalTxn(parentID types.FileContractID, parent types.V2FileContract, r *types.V2FileContractRenewal) types.V2Transaction {
	return types.V2Transaction{
		FileContractResolutions: []types.V2FileContractResolution{{
			Parent:     types.V2FileContractElement{ID: parentID, V2FileContract: parent},
			Resolution: r,
		}},
	}
}

// staleParent constructs an on-chain parent that has no revisions broadcast
// since formation. It pays a 7 SC ContractPrice up front, locks 100 SC of host
// collateral (all initially unrisked), and locks 80 SC of renter allowance.
// Sum is 187.
//
// The "latest off-chain" state (not directly stored on-chain) is what every
// renewal fixture in this file models — 30 SC of revenue has accrued, 40 SC
// of collateral has become risked, leaving HostOutput.Value=130 and
// RenterOutput.Value=57 (still summing to 187). renewalRevenue must recover
// the latest 30 SC of RiskedHostRevenue from the resolution, not the stale
// 7 SC parent.RiskedHostRevenue().
func staleParent() types.V2FileContract {
	return testFileContract(100, 110, 64, testCurrency(80), testCurrency(100), testCurrency(100), testCurrency(7))
}

func TestParseDiffsPartialRefreshCarriesHostRevenue(t *testing.T) {
	// Models rhp4 RefreshContractPartialRollover with full host-side rollover
	// (rp.Collateral=80 ≥ latest.MissedHostValue=60 case). The renter requests
	// 80 SC of new collateral, 50 SC of new allowance, ContractPrice=7. Per
	// rhp.go:993-1047 the new contract carries forward all 30 SC of latest
	// host revenue, so only the 7 SC ContractPrice is new renter spend.
	parent := staleParent()
	newContract := testFileContract(100, 110, 64, testCurrency(50), testCurrency(120), testCurrency(80), testCurrency(37))
	renewal := &types.V2FileContractRenewal{
		FinalRenterOutput: types.SiacoinOutput{Value: types.ZeroCurrency},
		FinalHostOutput:   types.SiacoinOutput{Value: types.ZeroCurrency},
		RenterRollover:    testCurrency(57),  // latest renter side
		HostRollover:      testCurrency(130), // latest host side
		NewContract:       newContract,
	}
	assertRenewalSumConserved(t, parent, renewal)

	state := testParseDiffs(t, []types.V2Transaction{renewalTxn(testContractID(2), parent, renewal)})

	if len(state.Formations) != 1 || len(state.Resolutions) != 1 {
		t.Fatalf("expected one formation and one resolution, got %d/%d", len(state.Formations), len(state.Resolutions))
	}
	// Pre-fix, the on-chain parent's RiskedHostRevenue=7 clipped the rolled
	// revenue to 7, so RenterContractPrice was reported as 30 (37 - 7) instead
	// of 7 (37 - 30). The fix uses FinalHostOutput.Value + HostRollover -
	// parent.TotalCollateral = 0 + 130 - 100 = 30 for the latest revenue.
	assertCurrency(t, "refresh spend", state.Formations[0].RenterContractPrice, testCurrency(7))
	assertCurrency(t, "refresh earned revenue", state.Resolutions[0].HostEarnedRevenue, types.ZeroCurrency)
	assertCurrency(t, "refresh new potential revenue", state.Formations[0].HostPotentialRevenue, testCurrency(37))
	// 23 SC of off-chain accrual (latest=30 minus stale=7) surfaces at the
	// renewal and gets credited to renter Spent.
	assertCurrency(t, "refresh deferred spend", state.Resolutions[0].RenterDeferredSpend, testCurrency(23))
}

func TestParseDiffsFullRefreshCarriesHostRevenue(t *testing.T) {
	// Models rhp4 RefreshContractFullRollover: rp.Collateral=10, rp.Allowance=18,
	// ContractPrice=10. The new contract's renter/host outputs are the latest
	// off-chain values plus the renter's new contributions. All 30 SC of latest
	// revenue rolls forward; only the 10 SC ContractPrice is new renter spend.
	parent := staleParent()
	newContract := testFileContract(100, 110, 64, testCurrency(75), testCurrency(110), testCurrency(70), testCurrency(40))
	renewal := &types.V2FileContractRenewal{
		FinalRenterOutput: types.SiacoinOutput{Value: types.ZeroCurrency},
		FinalHostOutput:   types.SiacoinOutput{Value: types.ZeroCurrency},
		RenterRollover:    testCurrency(57),
		HostRollover:      testCurrency(130),
		NewContract:       newContract,
	}
	assertRenewalSumConserved(t, parent, renewal)

	state := testParseDiffs(t, []types.V2Transaction{renewalTxn(testContractID(3), parent, renewal)})

	assertCurrency(t, "full refresh spend", state.Formations[0].RenterContractPrice, testCurrency(10))
	assertCurrency(t, "full refresh earned revenue", state.Resolutions[0].HostEarnedRevenue, types.ZeroCurrency)
	assertCurrency(t, "full refresh deferred spend", state.Resolutions[0].RenterDeferredSpend, testCurrency(23))
}

func TestParseDiffsPartialRefreshReleasesUnriskedCollateral(t *testing.T) {
	// Edge case in rhp4 RefreshContractPartialRollover: when
	// rp.Collateral < latest.MissedHostValue, HostRollover = hostFunds <
	// latest.HostOutput.Value and FinalHostOutput.Value > 0. The non-zero
	// FinalHostOutput.Value is the host taking back excess unrisked
	// collateral, NOT earned revenue — the latest RiskedHostRevenue still
	// rolls fully into the new contract.
	//
	// Fixture: parent matches its latest off-chain state (no staleness here),
	// with TC=100, MissedHostValue=80, revenue=0. Refresh with rp.Collateral=50
	// (< MissedHostValue=80), rp.Allowance=70, ContractPrice=5. Per
	// rhp.go:1011-1027: HostRollover = hostFunds = 0+20+50 = 70, so
	// FinalHostOutput.Value = 100-70 = 30 (= MissedHostValue - rp.Collateral).
	parent := testFileContract(100, 110, 64, testCurrency(80), testCurrency(100), testCurrency(80), testCurrency(0))
	newContract := testFileContract(100, 110, 64, testCurrency(70), testCurrency(70), testCurrency(50), testCurrency(5))
	renewal := &types.V2FileContractRenewal{
		FinalRenterOutput: types.SiacoinOutput{Value: testCurrency(5)},
		FinalHostOutput:   types.SiacoinOutput{Value: testCurrency(30)},
		RenterRollover:    testCurrency(75),
		HostRollover:      testCurrency(70),
		NewContract:       newContract,
	}
	assertRenewalSumConserved(t, parent, renewal)

	state := testParseDiffs(t, []types.V2Transaction{renewalTxn(testContractID(9), parent, renewal)})

	// FinalHostOutput.Value=30 is released unrisked collateral, not earned
	// revenue. Earned must be zero; only the new ContractPrice (5) is new spend.
	assertCurrency(t, "partial refresh spend", state.Formations[0].RenterContractPrice, testCurrency(5))
	assertCurrency(t, "partial refresh earned revenue", state.Resolutions[0].HostEarnedRevenue, types.ZeroCurrency)
	// Parent has revenue=0 on-chain and the latest (= same) is also 0, so no
	// off-chain accrual surfaces at this resolution.
	assertCurrency(t, "partial refresh deferred spend", state.Resolutions[0].RenterDeferredSpend, types.ZeroCurrency)
}

func TestParseDiffsTermRenewalSettlesOldRevenue(t *testing.T) {
	// Models rhp4 RenewContract (duration extension). new.ProofHeight differs
	// from parent.ProofHeight, so renewalCarriesRevenue is false: nothing rolls
	// forward and the full latest RiskedHostRevenue settles at this resolution.
	//
	// HostRollover = min(parent.TC=100, new.TC=200) = 100, so
	// FinalHostOutput.Value = latest.HostOutput.Value(130) - HostRollover(100) = 30.
	// renewalRevenue must recover the 30 SC of latest revenue from
	// FinalHostOutput.Value + HostRollover - parent.TC, NOT from the stale 7 SC
	// in parent.RiskedHostRevenue().
	parent := staleParent()
	newContract := testFileContract(200, 210, 64, testCurrency(75), testCurrency(200), testCurrency(170), testCurrency(25))
	renewal := &types.V2FileContractRenewal{
		FinalRenterOutput: types.SiacoinOutput{Value: types.ZeroCurrency},
		FinalHostOutput:   types.SiacoinOutput{Value: testCurrency(30)},
		RenterRollover:    testCurrency(57),
		HostRollover:      testCurrency(100),
		NewContract:       newContract,
	}
	assertRenewalSumConserved(t, parent, renewal)

	state := testParseDiffs(t, []types.V2Transaction{renewalTxn(testContractID(4), parent, renewal)})

	// Renter pays full new ContractPrice (25) for the new term; host realizes
	// the full latest revenue (30). Pre-fix, earned was reported as the stale
	// parent.RiskedHostRevenue()=7.
	assertCurrency(t, "renewal spend", state.Formations[0].RenterContractPrice, testCurrency(25))
	assertCurrency(t, "renewal earned revenue", state.Resolutions[0].HostEarnedRevenue, testCurrency(30))
	// 23 SC of off-chain accrual surfaced into earnedRevenue at this term
	// renewal; the renter's Spent gets the same 23 credited so the lineage
	// telescopes (renter Σ Spent = host Σ EarnedRevenue across renewals).
	assertCurrency(t, "renewal deferred spend", state.Resolutions[0].RenterDeferredSpend, testCurrency(23))
}

func TestParseDiffsRenewalMarksFormationFromRenewal(t *testing.T) {
	// A renewal-created formation must carry FromRenewal=true so the SQLite
	// layer knows not to count its filesize as new bytes uploaded. A fresh
	// formation (no renewal context) must leave the flag unset.
	parent := staleParent()
	newContract := testFileContract(100, 110, 64, testCurrency(50), testCurrency(120), testCurrency(80), testCurrency(37))
	renewal := &types.V2FileContractRenewal{
		FinalRenterOutput: types.SiacoinOutput{Value: types.ZeroCurrency},
		FinalHostOutput:   types.SiacoinOutput{Value: types.ZeroCurrency},
		RenterRollover:    testCurrency(57),
		HostRollover:      testCurrency(130),
		NewContract:       newContract,
	}
	assertRenewalSumConserved(t, parent, renewal)

	freshFC := testFileContract(100, 110, 64, testCurrency(100), testCurrency(200), testCurrency(200), testCurrency(7))

	// Two transactions in block order: the renewal first, then the fresh
	// formation. The renewal-created formation is appended at resolution
	// processing time, so state.Formations[0] is the renewal-created one.
	state := testParseDiffs(t, []types.V2Transaction{
		renewalTxn(testContractID(7), parent, renewal),
		{FileContracts: []types.V2FileContract{freshFC}},
	})

	if len(state.Formations) != 2 {
		t.Fatalf("expected two formations, got %d", len(state.Formations))
	}
	if !state.Formations[0].FromRenewal {
		t.Fatalf("renewal-created formation: expected FromRenewal=true")
	}
	if state.Formations[1].FromRenewal {
		t.Fatalf("fresh formation: expected FromRenewal=false")
	}
}

func TestParseDiffsAbandonsRenewalWithRegressedRevenue(t *testing.T) {
	// rhp4 v2 revisions only ever move value from renter side to host side; the
	// host never refunds. If a renewal's reconstructed latest off-chain
	// RiskedHostRevenue is below the on-chain parent's, the renewal wasn't
	// produced by rhp4 and the lineage can't be trusted — parseDiffs abandons.
	//
	// Fixture: on-chain parent has revenue=30 (formation captured everything).
	// Renewal's resolution implies latest=10 (host gave 20 SC back somehow).
	// Parent sum is 50 + 130 = 180; renewal sum is 0 + (10+100) + 50 + 20 = 180.
	parent := testFileContract(100, 110, 64, testCurrency(50), testCurrency(100), testCurrency(60), testCurrency(30))
	newContract := testFileContract(100, 110, 64, testCurrency(50), testCurrency(120), testCurrency(80), testCurrency(15))
	renewal := &types.V2FileContractRenewal{
		FinalRenterOutput: types.SiacoinOutput{Value: testCurrency(20)},
		FinalHostOutput:   types.SiacoinOutput{Value: types.ZeroCurrency},
		RenterRollover:    testCurrency(50),
		HostRollover:      testCurrency(110), // implies latest host value = 110, latest revenue = 10 (< parent's 30)
		NewContract:       newContract,
	}
	assertRenewalSumConserved(t, parent, renewal)

	state := testParseDiffs(t, []types.V2Transaction{renewalTxn(testContractID(12), parent, renewal)})

	if len(state.Resolutions) != 1 {
		t.Fatalf("expected one resolution, got %d", len(state.Resolutions))
	}
	if state.Resolutions[0].Type != ResolutionTypeAbandoned {
		t.Fatalf("resolution type: got %d, want %d (abandoned)", state.Resolutions[0].Type, ResolutionTypeAbandoned)
	}
	if len(state.Formations) != 0 {
		t.Fatalf("expected no formations from an abandoned renewal, got %d", len(state.Formations))
	}
	// Abandoned renewals must not credit EarnedRevenue or RenterDeferredSpend.
	assertCurrency(t, "abandoned earned revenue", state.Resolutions[0].HostEarnedRevenue, types.ZeroCurrency)
	assertCurrency(t, "abandoned deferred spend", state.Resolutions[0].RenterDeferredSpend, types.ZeroCurrency)
}

func TestParseDiffsRevisionAndResolutionBothProcessed(t *testing.T) {
	old := testFileContract(100, 110, 64, testCurrency(50), testCurrency(100), testCurrency(100), testCurrency(5))
	revised := testFileContract(100, 110, 128, testCurrency(45), testCurrency(100), testCurrency(95), testCurrency(10))
	revised.RevisionNumber = old.RevisionNumber + 1
	id := testContractID(5)
	state := testParseDiffs(t, []types.V2Transaction{
		{
			FileContractRevisions: []types.V2FileContractRevision{{
				Parent:   types.V2FileContractElement{ID: id, V2FileContract: old},
				Revision: revised,
			}},
		},
		{
			FileContractResolutions: []types.V2FileContractResolution{{
				Parent:     types.V2FileContractElement{ID: id, V2FileContract: revised},
				Resolution: &types.V2StorageProof{},
			}},
		},
	})

	if len(state.Revisions) != 1 {
		t.Fatalf("expected one revision, got %d", len(state.Revisions))
	} else if len(state.Resolutions) != 1 {
		t.Fatalf("expected one resolution, got %d", len(state.Resolutions))
	}
}
