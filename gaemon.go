package gaemon

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type Service interface {
	Serve(ctx context.Context) error
	Reload() error
}

type Gaemon struct {
	ctx             context.Context
	cancel          context.CancelFunc
	restartInterval time.Duration
	errorCallback   func(error)
	services        sync.Map
}

type Option func(g *Gaemon)

func WithService(srv Service) Option {
	return func(g *Gaemon) {
		ctx := context.WithValue(g.ctx, struct{}{}, nil)
		g.services.Store(ctx, srv)
	}
}

func WithRestartInterval(interval time.Duration) Option {
	return func(g *Gaemon) {
		g.restartInterval = interval
	}
}

func WithErrorCallback(cb func(error)) Option {
	return func(g *Gaemon) {
		g.errorCallback = cb
	}
}

func New(ctx context.Context, opts ...Option) *Gaemon {
	g := &Gaemon{
		restartInterval: time.Second,
	}

	g.ctx, g.cancel = context.WithCancel(ctx)

	for _, opt := range opts {
		opt(g)
	}

	return g
}

func (g *Gaemon) reload() {
	g.services.Range(func(key, value any) bool {
		ctx := key.(context.Context)
		srv := value.(Service)

		go func() {
			select {
			case <-ctx.Done():
			default:
				err := srv.Reload()
				if g.errorCallback != nil {
					g.errorCallback(err)
				}
			}
		}()

		return true
	})
}

func (g *Gaemon) Run() {
	var wg sync.WaitGroup
	g.services.Range(func(key, value any) bool {
		ctx := key.(context.Context)
		srv := value.(Service)

		wg.Add(1)
		go func() {
			defer wg.Done()

			ticker := time.NewTicker(g.restartInterval)
			defer ticker.Stop()

			for {
				err := srv.Serve(ctx)
				if g.errorCallback != nil {
					g.errorCallback(err)
				}

				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()

		return true
	})

	go func() {
		signalChan := make(chan os.Signal, 1)
		signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

		firstSigTerm := true
		for c := range signalChan {
			switch c {
			case syscall.SIGINT, syscall.SIGTERM:
				if firstSigTerm {
					g.cancel()
				} else {
					// force exit on repeated terminate signals
					os.Exit(1)
				}
			case syscall.SIGHUP:
				g.reload()
			}
		}
	}()

	wg.Wait()
}
