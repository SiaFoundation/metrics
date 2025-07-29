package main

import (
	"context"
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/gateway"
	"go.sia.tech/core/types"
	"go.sia.tech/coreutils"
	"go.sia.tech/coreutils/chain"
	"go.sia.tech/coreutils/syncer"
	"go.sia.tech/coreutils/testutil"
	"go.sia.tech/metrics/api"
	"go.sia.tech/metrics/metrics"
	"go.sia.tech/metrics/persist/sqlite"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	var (
		dir      string
		logLevel zap.AtomicLevel
		network  string
	)
	flag.StringVar(&dir, "dir", ".", "Directory to store metrics data")
	flag.StringVar(&network, "network", "mainnet", "Network to connect to (e.g. mainnet, testnet)")
	flag.TextVar(&logLevel, "log.level", zap.NewAtomicLevelAt(zap.InfoLevel), "Set the logging level (e.g. debug, info, warn, error)")
	flag.Parse()

	if err := os.MkdirAll(dir, 0755); err != nil {
		panic("failed to create directory: " + err.Error())
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	cfg := zap.NewProductionConfig()
	cfg.Level = logLevel
	cfg.EncoderConfig.EncodeTime = zapcore.RFC3339TimeEncoder
	cfg.OutputPaths = []string{"stdout"}
	log, err := cfg.Build()
	if err != nil {
		panic(err)
	}
	defer log.Sync()

	store, err := sqlite.OpenDatabase(filepath.Join(dir, "metrics.sqlite3"), sqlite.WithLogger(log.Named("sqlite3")))
	if err != nil {
		log.Panic("failed to open metrics database", zap.Error(err))
	}
	defer store.Close()

	var n *consensus.Network
	var genesis types.Block
	var peers []string
	switch network {
	case "mainnet":
		n, genesis = chain.Mainnet()
		peers = syncer.MainnetBootstrapPeers
	case "zen":
		n, genesis = chain.TestnetZen()
		peers = syncer.ZenBootstrapPeers
	}

	db, err := coreutils.OpenBoltChainDB(filepath.Join(dir, "consensus.db"))
	if err != nil {
		log.Panic("failed to open consensus database", zap.Error(err))
	}
	defer db.Close()

	chainStore, cs, err := chain.NewDBStore(db, n, genesis, nil)
	if err != nil {
		log.Panic("failed to create chain store", zap.Error(err))
	}

	cm := chain.NewManager(chainStore, cs, chain.WithLog(log.Named("chain")))

	syncerListener, err := net.Listen("tcp", ":9981")
	if err != nil {
		log.Panic("failed to start syncer listener", zap.Error(err))
	}
	defer syncerListener.Close()

	s := syncer.New(syncerListener, cm, testutil.NewEphemeralPeerStore(), gateway.Header{
		GenesisID:  genesis.ID(),
		UniqueID:   gateway.GenerateUniqueID(),
		NetAddress: net.JoinHostPort("127.0.0.1", "9981"),
	}, syncer.WithLogger(log.Named("syncer")))
	defer s.Close()
	go s.Run()

	for _, peer := range peers {
		go func(peer string) {
			if _, err := s.Connect(context.Background(), peer); err != nil {
				log.Error("failed to connect to peer", zap.String("peer", peer), zap.Error(err))
			}
		}(peer)
	}

	metrics, err := metrics.NewManager(cm, store, log.Named("metrics"))
	if err != nil {
		log.Panic("failed to create metrics manager", zap.Error(err))
	}
	defer metrics.Close()

	l, err := net.Listen("tcp", ":9980")
	if err != nil {
		log.Panic("failed to start metrics listener", zap.Error(err))
	}
	defer l.Close()

	srv := &http.Server{
		Handler: api.NewHandler(metrics),
		BaseContext: func(l net.Listener) context.Context {
			return ctx
		},
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second,
	}
	defer srv.Close()
	go func() {
		if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
			log.Error("failed to start metrics server", zap.Error(err))
		}
	}()

	<-ctx.Done()
	log.Info("shutting down metrics server")
	time.AfterFunc(30*time.Second, func() {
		log.Error("metrics server shutdown timed out, forcing exit")
		os.Exit(1)
	})
}
