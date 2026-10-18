package api

import (
	"context"
	"errors"
	"localconverter/internal/camera"
	"log"
	"net/http"
	"time"
)

// Run blocks until the listener fails or the shutdown sequence completes.
func Run(ctx context.Context, listen string, logger *log.Logger, manager *camera.Manager, media *camera.Media, heartbeatSeconds int, origins []string, token string) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	runtime := NewRuntime(manager, media, listen, heartbeatSeconds, token)
	runtime.AllowedOrigins = origins
	runtime.Shutdown = stop
	server := &http.Server{
		Addr: listen, Handler: NewHandler(logger, runtime), ErrorLog: logger,
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
	}
	result := make(chan error, 1)
	go func() {
		logger.Printf("starting localconverter on http://%s", listen)
		result <- server.ListenAndServe()
	}()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		if err := <-result; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		logger.Print("localconverter stopped")
		return nil
	}
}
