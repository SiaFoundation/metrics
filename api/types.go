package api

import (
	"time"

	"go.sia.tech/core/types"
)

// UsageSummaryResponse is a one-shot summary of network activity over a
// requested period. Period-scoped counts ("active" / "new") are computed
// directly from the indexer's per-key activity table; cumulative counters
// are reported as deltas between the start- and end-of-period snapshots.
type UsageSummaryResponse struct {
	// Distinct keys with at least one chain event in [start, end].
	ActiveHosts   uint64 `json:"activeHosts"`
	ActiveRenters uint64 `json:"activeRenters"`
	// Distinct keys whose first chain event ever occurred in [start, end].
	NewHosts   uint64 `json:"newHosts"`
	NewRenters uint64 `json:"newRenters"`

	// Period deltas of cumulative counters: end-of-period minus start-of-period.
	NewContracts     uint64         `json:"newContracts"`
	Transactions     uint64         `json:"transactions"`
	Revisions        uint64         `json:"revisions"`
	BytesUploaded    uint64         `json:"bytesUploaded"`
	ActiveByteDays   uint64         `json:"activeByteDays"`
	RenterSpending   types.Currency `json:"renterSpending"`
	HostEarnedRevenue types.Currency `json:"hostEarnedRevenue"`
	BurntCollateral  types.Currency `json:"burntCollateral"`
	Tax              types.Currency `json:"tax"`

	// TVL at the end of the period (locked allowance + locked collateral).
	TVL types.Currency `json:"tvl"`

	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}
