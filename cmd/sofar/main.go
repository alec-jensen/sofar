// Command sofar is the single binary: web/API server, background worker,
// migrations and small admin tools.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"
	_ "time/tzdata" // zone data even where the OS has none (scratch containers, Windows)

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/alec-jensen/sofar/internal/api"
	"github.com/alec-jensen/sofar/internal/auth"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/config"
	"github.com/alec-jensen/sofar/internal/crypt"
	"github.com/alec-jensen/sofar/internal/jobs"
	"github.com/alec-jensen/sofar/internal/logging"
	"github.com/alec-jensen/sofar/internal/metrics"
	"github.com/alec-jensen/sofar/internal/netguard"
	"github.com/alec-jensen/sofar/internal/notify"
	"github.com/alec-jensen/sofar/internal/recompute"
	"github.com/alec-jensen/sofar/internal/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: sofar <command>

  serve [--api=true] [--worker=true]   run the web/API server and/or background worker
  migrate up|status                    apply or list database migrations
  recompute [--all | --from YYYY-MM]   rebuild month snapshots (default: whatever is dirty)
  vapid                                print a new pair of Web Push keys`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "vapid":
		err = vapid()
	case "serve":
		err = serve(os.Args[2:])
	case "migrate":
		err = migrate(os.Args[2:])
	case "recompute":
		err = recomputeCmd(os.Args[2:])
	case "help", "-h", "--help":
		fmt.Println(usage)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sofar:", err)
		os.Exit(1)
	}
}

func vapid() error {
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return err
	}
	fmt.Printf("VAPID_PUBLIC_KEY=%s\nVAPID_PRIVATE_KEY=%s\n", public, private)
	return nil
}

func load() (config.Config, error) {
	return config.Load(os.Getenv, os.ReadFile)
}

func migrate(args []string) error {
	if len(args) != 1 || (args[0] != "up" && args[0] != "status") {
		return errors.New("usage: sofar migrate up|status")
	}
	cfg, err := load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if args[0] == "up" {
		if err = store.Migrate(ctx, db); err != nil {
			return err
		}
	}
	st, err := store.MigrationStatus(ctx, db)
	if err != nil {
		return err
	}
	sort.Slice(st, func(i, j int) bool { return st[i].Version < st[j].Version })
	for _, s := range st {
		mark := "pending"
		if s.Applied {
			mark = "applied"
		}
		fmt.Printf("%05d  %-8s %s\n", s.Version, mark, s.Source)
	}
	return nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	runAPI := fs.Bool("api", true, "serve the web app and API")
	runWorker := fs.Bool("worker", true, "run background jobs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*runAPI && !*runWorker {
		return errors.New("nothing to run: enable --api and/or --worker")
	}
	cfg, err := load()
	if err != nil {
		return err
	}
	log := logging.New(os.Stderr, cfg.LogLevel)
	for _, w := range cfg.Warnings() {
		log.Warn().Msg(w)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	startup, cancel := context.WithTimeout(ctx, time.Minute)
	db, err := store.Open(startup, cfg.DatabaseURL)
	if err == nil {
		err = store.Migrate(startup, db)
	}
	cancel()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer db.Close()

	m := metrics.New(version)
	if *runWorker {
		q := jobs.NewQueue(db, log)
		q.OnResult = m.JobDone
		sched := jobs.NewScheduler(db, log, cfg.HomeTZ)
		go q.Run(ctx, 5*time.Second)
		go sched.Run(ctx, time.Minute)
		log.Info().Msg("worker started")
	}
	if !*runAPI {
		<-ctx.Done()
		return nil
	}

	box, err := crypt.New(cfg.EncryptionKey)
	if err != nil {
		return err
	}
	h := api.New(api.WithDomain(api.Deps{
		Cfg:     cfg,
		DB:      db,
		Auth:    auth.New(db, box, cfg.SetupKey),
		Push:    &notify.Sender{DB: db, Client: netguard.NewOutboundClient(), Public: cfg.VAPIDPublic, Private: cfg.VAPIDPrivate, Subject: orDefault(cfg.VAPIDSubject, "mailto:admin@example.com"), Log: log},
		Log:     log,
		Metrics: m.Handler(),
		Ready: func(ctx context.Context) error {
			if err := db.PingContext(ctx); err != nil {
				return err
			}
			ok, err := store.Current(ctx, db)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("migrations pending")
			}
			return nil
		},
	}))
	srv := &http.Server{Addr: cfg.Bind, Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	go func() {
		<-ctx.Done()
		c, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		srv.Shutdown(c)
	}()
	log.Info().Str("address", srv.Addr).Msg("sofar is ready")
	if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func orDefault(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func recomputeCmd(args []string) error {
	fs := flag.NewFlagSet("recompute", flag.ContinueOnError)
	all := fs.Bool("all", false, "recompute every month from the first with data")
	from := fs.String("from", "", "recompute from this month (YYYY-MM) forward")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err = store.Migrate(ctx, db); err != nil {
		return err
	}
	svc := api.WithDomain(api.Deps{Cfg: cfg, DB: db}).Recompute
	var res recompute.Result
	switch {
	case *all:
		res, err = svc.RecomputeAll(ctx)
	case *from != "":
		ym, perr := civil.ParseYearMonth(*from)
		if perr != nil {
			return perr
		}
		res, err = svc.RecomputeFrom(ctx, ym)
	default:
		res, err = svc.Recompute(ctx)
	}
	if err != nil {
		return err
	}
	fmt.Println("recomputed", len(res.Computed), "month(s)")
	return nil
}
