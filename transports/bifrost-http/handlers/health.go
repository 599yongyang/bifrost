package handlers

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// HealthHandler manages HTTP requests for health checks.
type HealthHandler struct {
	config *lib.Config
}

// NewHealthHandler creates a new health handler instance.
func NewHealthHandler(config *lib.Config) *HealthHandler {
	return &HealthHandler{
		config: config,
	}
}

// RegisterRoutes registers the health-related routes.
func (h *HealthHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/health", lib.ChainMiddlewares(h.getHealth, middlewares...))
}

// getHealth handles GET /api/health - Get the health status of the server.
func (h *HealthHandler) getHealth(ctx *fasthttp.RequestCtx) {
	// If DB pings are disabled, just return OK
	if h.config.ClientConfig.DisableDBPingsInHealth {
		SendJSON(ctx, map[string]any{"status": "ok", "components": map[string]any{"db_pings": "disabled"}})
		return
	}
	// Pinging config store
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var errors []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	if h.config.ConfigStore != nil {
		wg.Add(1)
		go runHealthProbe(&wg, &mu, &errors, "config store", func() error { return h.config.ConfigStore.Ping(reqCtx) })
	}

	// Pinging log store
	if h.config.LogsStore != nil {
		wg.Add(1)
		go runHealthProbe(&wg, &mu, &errors, "log store", func() error { return h.config.LogsStore.Ping(reqCtx) })
	}

	// Pinging vector store
	if h.config.VectorStore != nil {
		wg.Add(1)
		go runHealthProbe(&wg, &mu, &errors, "vector store", func() error { return h.config.VectorStore.Ping(reqCtx) })
	}

	wg.Wait()

	if len(errors) > 0 {
		SendError(ctx, fasthttp.StatusServiceUnavailable, errors[0])
		return
	}
	SendJSON(ctx, map[string]any{"status": "ok", "components": map[string]any{"db_pings": "ok"}})
}

func runHealthProbe(wg *sync.WaitGroup, mu *sync.Mutex, errors *[]string, name string, probe func() error) {
	defer wg.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			if logger != nil {
				logger.Error("recovered health probe panic: component=%s panic_type=%T\n%s", name, recovered, debug.Stack())
			}
			mu.Lock()
			*errors = append(*errors, fmt.Sprintf("%s not available", name))
			mu.Unlock()
		}
	}()
	if err := probe(); err != nil {
		mu.Lock()
		*errors = append(*errors, fmt.Sprintf("%s not available", name))
		mu.Unlock()
	}
}
