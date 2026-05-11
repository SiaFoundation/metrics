package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.sia.tech/core/types"
	"go.sia.tech/jape"
	"go.sia.tech/metrics/metrics"
)

type (
	// Chain defines the interface for accessing blockchain state.
	Chain interface {
		Tip() types.ChainIndex
	}

	// Metrics defines the interface for accessing metrics data.
	Metrics interface {
		HostMetric(context.Context, types.PublicKey, time.Time) (metrics.Host, error)
		RenterMetric(context.Context, types.PublicKey, time.Time) (metrics.Renter, error)
		GlobalMetric(context.Context, time.Time) (metrics.Metrics, error)

		TopHosts(context.Context, time.Time, time.Time, int) ([]metrics.Host, error)
		TopRenters(context.Context, time.Time, time.Time, int) ([]metrics.Renter, error)

		HostsCount(context.Context, time.Time, time.Time) (int64, error)
		RentersCount(context.Context, time.Time, time.Time) (int64, error)
		NewHosts(context.Context, time.Time, time.Time) (int64, error)
		NewRenters(context.Context, time.Time, time.Time) (int64, error)

		HostMetrics(context.Context, types.PublicKey, time.Time, time.Time) ([]metrics.Host, error)
		RenterMetrics(context.Context, types.PublicKey, time.Time, time.Time) ([]metrics.Renter, error)
		GlobalMetrics(context.Context, time.Time, time.Time) ([]metrics.Metrics, error)
	}

	api struct {
		metrics Metrics
		chain   Chain
	}
)

func (a *api) handleConsensusTip(jc jape.Context) {
	jc.Encode(a.chain.Tip())
}

func (a *api) handleHostsKeyLast(jc jape.Context) {
	ctx := jc.Request.Context()
	var hostKey types.PublicKey
	if err := jc.DecodeParam("key", &hostKey); err != nil {
		return
	}

	h, err := a.metrics.HostMetric(ctx, hostKey, time.Now())
	if errors.Is(err, metrics.ErrNotFound) {
		jc.Error(metrics.ErrNotFound, http.StatusNotFound)
		return
	} else if jc.Check("failed to get last host metrics", err) != nil {
		return
	}
	jc.Encode(h)
}

func (a *api) handleRentersKeyLast(jc jape.Context) {
	ctx := jc.Request.Context()
	var renterKey types.PublicKey
	if err := jc.DecodeParam("key", &renterKey); err != nil {
		return
	}

	h, err := a.metrics.RenterMetric(ctx, renterKey, time.Now())
	if errors.Is(err, metrics.ErrNotFound) {
		jc.Error(metrics.ErrNotFound, http.StatusNotFound)
		return
	} else if jc.Check("failed to get last renter metrics", err) != nil {
		return
	}
	jc.Encode(h)
}

func (a *api) handleMetricsLast(jc jape.Context) {
	ctx := jc.Request.Context()

	m, err := a.metrics.GlobalMetric(ctx, time.Now())
	if errors.Is(err, metrics.ErrNotFound) {
		jc.Error(metrics.ErrNotFound, http.StatusNotFound)
		return
	} else if jc.Check("failed to get last global metrics", err) != nil {
		return
	}
	jc.Encode(m)
}

// handleMetrics returns the global metrics time series in [start, end].
// Both bounds are optional: if neither is provided the window defaults to
// the last 30 days truncated to the hour, matching the rest of the API.
// Snapshots are hourly, ordered by timestamp ascending.
func (a *api) handleMetrics(jc jape.Context) {
	ctx := jc.Request.Context()

	end := time.Now().Truncate(time.Hour)
	start := end.AddDate(0, -1, 0) // one month ago
	if err := jc.DecodeForm("start", &start); err != nil {
		return
	}
	if err := jc.DecodeForm("end", &end); err != nil {
		return
	}
	if !end.After(start) {
		jc.Error(errors.New("end must be after start"), http.StatusBadRequest)
		return
	}

	ms, err := a.metrics.GlobalMetrics(ctx, start, end)
	if jc.Check("failed to get global metrics", err) != nil {
		return
	}
	jc.Encode(ms)
}

// handleHostMetrics returns the per-host metrics time series in [start, end].
// Bounds default to the last 30 days truncated to the hour, matching the
// /metrics endpoint. Snapshots are hourly, ordered by timestamp ascending.
func (a *api) handleHostMetrics(jc jape.Context) {
	ctx := jc.Request.Context()
	var hostKey types.PublicKey
	if err := jc.DecodeParam("key", &hostKey); err != nil {
		return
	}

	end := time.Now().Truncate(time.Hour)
	start := end.AddDate(0, -1, 0)
	if err := jc.DecodeForm("start", &start); err != nil {
		return
	}
	if err := jc.DecodeForm("end", &end); err != nil {
		return
	}
	if !end.After(start) {
		jc.Error(errors.New("end must be after start"), http.StatusBadRequest)
		return
	}

	hs, err := a.metrics.HostMetrics(ctx, hostKey, start, end)
	if jc.Check("failed to get host metrics", err) != nil {
		return
	}
	jc.Encode(hs)
}

// handleRenterMetrics mirrors handleHostMetrics for renters.
func (a *api) handleRenterMetrics(jc jape.Context) {
	ctx := jc.Request.Context()
	var renterKey types.PublicKey
	if err := jc.DecodeParam("key", &renterKey); err != nil {
		return
	}

	end := time.Now().Truncate(time.Hour)
	start := end.AddDate(0, -1, 0)
	if err := jc.DecodeForm("start", &start); err != nil {
		return
	}
	if err := jc.DecodeForm("end", &end); err != nil {
		return
	}
	if !end.After(start) {
		jc.Error(errors.New("end must be after start"), http.StatusBadRequest)
		return
	}

	rs, err := a.metrics.RenterMetrics(ctx, renterKey, start, end)
	if jc.Check("failed to get renter metrics", err) != nil {
		return
	}
	jc.Encode(rs)
}

func (a *api) handleTopHosts(jc jape.Context) {
	ctx := jc.Request.Context()
	end := time.Now().Truncate(time.Hour)
	start := end.AddDate(0, -1, 0) // one month ago

	hosts, err := a.metrics.TopHosts(ctx, start, end, 50)
	if jc.Check("failed to get top hosts", err) != nil {
		return
	}
	jc.Encode(hosts)
}

func (a *api) handleTopRenters(jc jape.Context) {
	ctx := jc.Request.Context()
	end := time.Now().Truncate(time.Hour)
	start := end.AddDate(0, -1, 0) // one month ago

	renters, err := a.metrics.TopRenters(ctx, start, end, 50)
	if jc.Check("failed to get top renters", err) != nil {
		return
	}
	jc.Encode(renters)
}

func (a *api) handleSummary(jc jape.Context) {
	ctx := jc.Request.Context()
	end := time.Now().Truncate(time.Hour)
	start := end.AddDate(0, -1, 0) // one month ago
	// Optional start/end query params let callers request a summary over a
	// custom window so the values can be aligned with /metrics?start=...&end=...
	// for the same period. Bounds default to the prior 30-day behavior.
	if err := jc.DecodeForm("start", &start); err != nil {
		return
	}
	if err := jc.DecodeForm("end", &end); err != nil {
		return
	}
	if !end.After(start) {
		jc.Error(errors.New("end must be after start"), http.StatusBadRequest)
		return
	}

	activeHosts, err := a.metrics.HostsCount(ctx, start, end)
	if jc.Check("failed to get active hosts count", err) != nil {
		return
	}
	activeRenters, err := a.metrics.RentersCount(ctx, start, end)
	if jc.Check("failed to get active renters count", err) != nil {
		return
	}
	newHosts, err := a.metrics.NewHosts(ctx, start, end)
	if jc.Check("failed to get new hosts count", err) != nil {
		return
	}
	newRenters, err := a.metrics.NewRenters(ctx, start, end)
	if jc.Check("failed to get new renters count", err) != nil {
		return
	}

	last, err := a.metrics.GlobalMetric(ctx, end)
	if jc.Check("failed to get last global metrics", err) != nil {
		return
	}
	first, err := a.metrics.GlobalMetric(ctx, start)
	if jc.Check("failed to get first global metrics", err) != nil {
		return
	}

	lastTotalContracts := last.ActiveContracts + last.FailedContracts + last.RenewedContracts + last.SuccessfulContracts
	firstTotalContracts := first.ActiveContracts + first.FailedContracts + first.RenewedContracts + first.SuccessfulContracts

	jc.Encode(UsageSummaryResponse{
		ActiveHosts:   uint64(activeHosts),
		ActiveRenters: uint64(activeRenters),
		NewHosts:      uint64(newHosts),
		NewRenters:    uint64(newRenters),

		NewContracts:      lastTotalContracts - firstTotalContracts,
		Transactions:      last.TransactionCount - first.TransactionCount,
		Revisions:         last.RevisionCount - first.RevisionCount,
		BytesUploaded:     last.BytesUploaded - first.BytesUploaded,
		ActiveByteDays:    last.ActiveByteDays - first.ActiveByteDays,
		RenterSpending:    last.SpentAllowance.Sub(first.SpentAllowance),
		HostEarnedRevenue: last.EarnedRevenue.Sub(first.EarnedRevenue),
		BurntCollateral:   last.BurntCollateral.Sub(first.BurntCollateral),
		Tax:               last.Tax.Sub(first.Tax),

		TVL: last.LockedAllowance.Add(last.LockedCollateral),

		Start: start,
		End:   end,
	})
}

func (a *api) handleDeltaDaysHosts(jc jape.Context) {
	ctx := jc.Request.Context()
	var days int
	err := jc.DecodeParam("days", &days)
	if err != nil {
		return
	}

	var hostKey types.PublicKey
	if err := jc.DecodeParam("key", &hostKey); err != nil {
		return
	}
	end := time.Now().Truncate(time.Hour)
	start := end.AddDate(0, 0, -days)

	first, err := a.metrics.HostMetric(ctx, hostKey, start)
	if jc.Check("failed to get delta hosts", err) != nil {
		return
	}
	last, err := a.metrics.HostMetric(ctx, hostKey, end)
	if jc.Check("failed to get delta hosts", err) != nil {
		return
	}

	jc.Encode([]metrics.Host{first, last})
}

func (a *api) handleDeltaDaysRenters(jc jape.Context) {
	ctx := jc.Request.Context()
	var days int
	err := jc.DecodeParam("days", &days)
	if err != nil {
		return
	}

	var renterKey types.PublicKey
	if err := jc.DecodeParam("key", &renterKey); err != nil {
		return
	}
	end := time.Now().Truncate(time.Hour)
	start := end.AddDate(0, 0, -days)

	first, err := a.metrics.RenterMetric(ctx, renterKey, start)
	if jc.Check("failed to get delta renters", err) != nil {
		return
	}
	last, err := a.metrics.RenterMetric(ctx, renterKey, end)
	if jc.Check("failed to get delta renters", err) != nil {
		return
	}

	jc.Encode([]metrics.Renter{first, last})
}

func (a *api) handleDeltaDaysMetrics(jc jape.Context) {
	ctx := jc.Request.Context()
	var days int
	err := jc.DecodeParam("days", &days)
	if err != nil {
		return
	}

	end := time.Now().Truncate(time.Hour)
	start := end.AddDate(0, 0, -days)

	first, err := a.metrics.GlobalMetric(ctx, start)
	if jc.Check("failed to get delta metrics", err) != nil {
		return
	}
	last, err := a.metrics.GlobalMetric(ctx, end)
	if jc.Check("failed to get delta metrics", err) != nil {
		return
	}

	jc.Encode([]metrics.Metrics{first, last})
}

// NewHandler creates a new HTTP handler for the metrics API.
func NewHandler(chain Chain, metrics Metrics) http.Handler {
	a := &api{
		chain:   chain,
		metrics: metrics,
	}

	return jape.Mux(map[string]jape.Handler{
		"GET /consensus/tip": a.handleConsensusTip,

		"GET /summary":                  a.handleSummary,
		"GET /top/hosts":                a.handleTopHosts,
		"GET /top/renters":              a.handleTopRenters,
		"GET /hosts/:key/last":          a.handleHostsKeyLast,
		"GET /hosts/:key/metrics":       a.handleHostMetrics,
		"GET /renters/:key/last":        a.handleRentersKeyLast,
		"GET /renters/:key/metrics":     a.handleRenterMetrics,
		"GET /metrics":                  a.handleMetrics,
		"GET /metrics/last":             a.handleMetricsLast,
		"GET /delta/:days/hosts/:key":   a.handleDeltaDaysHosts,
		"GET /delta/:days/renters/:key": a.handleDeltaDaysRenters,
		"GET /delta/:days/metrics":      a.handleDeltaDaysMetrics,
	})
}
