package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/IQVO/asrs-control/internal/adapters/inbound/http"
	"github.com/IQVO/asrs-control/internal/adapters/mqtt"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/events"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/kafka"
	outboxrelay "github.com/IQVO/asrs-control/internal/adapters/outbound/outbox"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/system"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	layoutfile "github.com/IQVO/asrs-control/internal/config"
	"github.com/IQVO/asrs-control/internal/domain/layout"
)

type app struct {
	log       *slog.Logger
	pool      *pgxpool.Pool
	svc       *usecases.Service
	layouts   []layout.Layout
	links     mqtt.Links
	relay     *outboxrelay.Relay
	sink      *kafka.RelaySink
	idem      *postgres.IdempotencyStore
	inbox     *postgres.TelegramInbox
	readiness *inboundhttp.Readiness
	server    *http.Server
}

func wire(ctx context.Context, cfg config, log *slog.Logger) (*app, error) {
	layouts, err := layoutfile.LoadLayouts(cfg.LayoutFile)
	if err != nil {
		return nil, err
	}
	if err := postgres.RunMigrations(cfg.MigrationsURL); err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	a := &app{log: log, pool: pool, layouts: layouts, links: mqtt.Links{}, idem: postgres.NewIdempotencyStore(pool), inbox: postgres.NewTelegramInbox(pool)}
	uow, outboxRepo := postgres.NewUnitOfWork(pool), postgres.NewOutboxRepo(pool)
	a.svc, err = usecases.New(usecases.Deps{Layouts: layouts, UoW: uow, Maps: postgres.NewStorageMapRepo(pool), Jobs: postgres.NewStorageJobRepo(pool),
		Units: postgres.NewUnitRepo(pool), Capacity: postgres.NewCapacityRepo(pool), Telegrams: a.inbox,
		Events: events.NewSink(outboxRepo, system.IDs{}.NewID), Downlink: a.links, Clock: system.Clock{}, IDs: system.IDs{},
		Velocity: cfg.Velocity, Log: log})
	if err == nil {
		err = a.svc.Prepare(ctx)
	}
	if err != nil {
		a.close()
		return nil, err
	}
	for _, l := range layouts {
		link, err := mqtt.NewLink(cfg.MQTTURL, cfg.MQTTClientID+"-"+l.ID, l.ID, a.svc, func() time.Time { return time.Now().UTC() }, log)
		if err != nil {
			a.close()
			return nil, err
		}
		a.links[l.ID] = link
	}
	a.sink = kafka.NewRelaySink(cfg.Brokers)
	a.relay = outboxrelay.NewOutboxRelay(outboxRepo, a.sink, log, outboxrelay.WithInterval(cfg.OutboxInterval),
		outboxrelay.WithBatchSize(cfg.OutboxBatch), outboxrelay.WithMaxAttempts(cfg.OutboxMaxAttempts))
	a.readiness = inboundhttp.NewReadiness(0, 0,
		inboundhttp.Dependency{Name: "postgres", Check: pool.Ping},
		inboundhttp.Dependency{Name: "kafka", Check: kafkaCheck(cfg.Brokers)},
		inboundhttp.Dependency{Name: "mqtt", Check: func(context.Context) error { return a.links.Connected() }})
	idem := inboundhttp.NewIdempotency(a.idem, uow, system.Clock{})
	router := inboundhttp.NewRouter(&inboundhttp.Server{Service: a.svc, Idempotency: idem.Middleware, Readiness: a.readiness}, log)
	a.server = &http.Server{Handler: router, ReadHeaderTimeout: 5 * time.Second}
	return a, nil
}

func (a *app) close() {
	for _, l := range a.links {
		l.Close()
	}
	if a.sink != nil {
		_ = a.sink.Close()
	}
	a.pool.Close()
}
