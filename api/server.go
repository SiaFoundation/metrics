package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/jape"
	"go.sia.tech/metrics/metrics"
)

type (
	// Chain defines the interface for accessing blockchain state.
	Chain interface {
		TipState() consensus.State
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

	hosts, err := a.metrics.HostsCount(ctx, start, end)
	if jc.Check("failed to get hosts count", err) != nil {
		return
	}

	renters, err := a.metrics.RentersCount(ctx, start, end)
	if jc.Check("failed to get renters count", err) != nil {
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
		ActiveHosts:   uint64(hosts),
		ActiveRenters: uint64(renters),
		NewHosts:      uint64(last.Hosts - first.Hosts),
		NewRenters:    uint64(last.Renters - first.Renters),

		RenterSpending: last.SpentAllowance.Sub(first.SpentAllowance),
		NewContracts:   lastTotalContracts - firstTotalContracts,

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
	log.Println(days, start, end)

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
	log.Println(days, start, end)

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
	log.Println(days, start, end)

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
func NewHandler(metrics Metrics) http.Handler {
	a := &api{
		metrics: metrics,
	}

	return jape.Mux(map[string]jape.Handler{
		"GET /consensus/tip": a.handleConsensusTip,

		"GET /summary":                  a.handleSummary,
		"GET /top/hosts":                a.handleTopHosts,
		"GET /top/renters":              a.handleTopRenters,
		"GET /hosts/:key/last":          a.handleHostsKeyLast,
		"GET /renters/:key/last":        a.handleRentersKeyLast,
		"GET /metrics/last":             a.handleMetricsLast,
		"GET /delta/:days/hosts/:key":   a.handleDeltaDaysHosts,
		"GET /delta/:days/renters/:key": a.handleDeltaDaysRenters,
		"GET /delta/:days/metrics":      a.handleDeltaDaysMetrics,
	})
}
