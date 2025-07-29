package api

import (
	"time"

	"go.sia.tech/core/types"
)

type UsageSummaryResponse struct {
	ActiveHosts   uint64 `json:"activeHosts"`
	ActiveRenters uint64 `json:"activeRenters"`

	NewHosts   uint64 `json:"newHosts"`
	NewRenters uint64 `json:"newRenters"`

	RenterSpending types.Currency `json:"renterSpending"`

	NewContracts uint64 `json:"newContracts"`

	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}
