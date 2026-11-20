package fiber

import (
	"context"
	"errors"
	"fmt"

	"github.com/astraive/loza/sdks/go"
	fiberpkg "github.com/gofiber/fiber/v2"
)

// Config controls fiber middleware behavior.
type Config struct {
	Event string
}

// Middleware starts and emits canonical events for fiber handlers.
func Middleware(cfg ...Config) fiberpkg.Handler {
	c := Config{}
	if len(cfg) > 0 {
		c = cfg[0]
	}
	return MiddlewareWithConfig(c)
}

// MiddlewareWithConfig starts and emits canonical events for fiber handlers.
func MiddlewareWithConfig(cfg Config) fiberpkg.Handler {
	eventName := cfg.Event
	if eventName == "" {
		eventName = "http.request"
	}

	return func(c *fiberpkg.Ctx) (err error) {
		ctx := loza.StartEvent(c.UserContext(), loza.Params{
			Event:  eventName,
			Method: c.Method(),
			Path:   c.Path(),
		})
		c.SetUserContext(ctx)

		defer func() {
			if rec := recover(); rec != nil {
				if !loza.PanicRecoveryEnabled() {
					// The panic is intentionally propagated to the caller, but the
					// event started above must still be finished and emitted so it
					// is not orphaned when recovery is disabled.
					loza.FinishError(ctx, panicErr{value: rec}, loza.Int("status_code", fiberpkg.StatusInternalServerError))
					_ = loza.Emit(ctx)
					panic(rec)
				}
				perr := panicErr{value: rec}
				// A recovered panic is a server error. Force the generic 500
				// status/body so a stale handler status is not served and the raw
				// panic value is never written to the response by Fiber's error
				// handler. SendStatus only writes a body when none exists yet, so
				// a partially written response is kept. Keep the detailed panic
				// value in telemetry only: returning it as the handler error would
				// make Fiber's error handler disclose it in the response body.
				status := effectiveStatus(perr)
				_ = c.SendStatus(status)
				enrichRoute(ctx, c)
				loza.FinishError(ctx, perr, loza.Int("status_code", status))
				_ = loza.Emit(ctx)
				err = nil
			}
		}()

		err = c.Next()
		route := ""
		if !isUnmatchedRoute(err) {
			if matched := c.Route(); matched != nil {
				route = matched.Path
			}
		}
		if route != "" {
			loza.Set(ctx, loza.Route(route))
		}
		if err != nil {
			status := effectiveStatus(err)
			c.Status(status)
			loza.FinishError(ctx, err, loza.Int("status_code", status))
		} else {
			loza.Finish(ctx, "success", loza.Int("status_code", c.Response().StatusCode()))
		}
		_ = loza.Emit(ctx)
		return err
	}
}

// effectiveStatus reports the status Fiber's default error handler will serve
// for err: the code of a *fiber.Error, or 500 for any other error. The middleware
// records this value instead of the response status snapshot because Fiber
// invokes its error handler after the middleware returns and overwrites the
// handler's status. Recording the snapshot would emit a status_code that does
// not match the status the client actually receives.
func effectiveStatus(err error) int {
	var fiberErr *fiberpkg.Error
	if errors.As(err, &fiberErr) {
		return fiberErr.Code
	}
	return fiberpkg.StatusInternalServerError
}

// enrichRoute attaches the matched fiber route to the event when available.
// It is shared by the normal and panic paths so route enrichment is not skipped
// when a handler panics and control unwinds before the post-Next code. Route is
// a canonical field, so it must be Set (not Enriched) to survive emit-time
// duplicate-field processing.
func enrichRoute(ctx context.Context, c *fiberpkg.Ctx) {
	if c.Route() == nil {
		return
	}
	if route := c.Route().Path; route != "" {
		_ = loza.Set(ctx, loza.Route(route))
	}
}

type panicErr struct {
	value any
}

func (e panicErr) Error() string {
	return fmt.Sprintf("panic recovered: %v", e.value)
}

// isUnmatchedRoute reports whether err is the error Fiber's router produces
// when no registered route matched the request (404 Not Found) or when the
// request method is not allowed for a matched path (405 Method Not Allowed).
//
// Fiber's Ctx.Route() never returns nil: for an unmatched request it falls
// back to a synthetic Route whose Path is the raw original request path.
// Recording that value creates high-cardinality telemetry and can leak
// path-segment values, so unmatched requests are detected here instead of
// relying on an always-true nil check.
func isUnmatchedRoute(err error) bool {
	var fiberErr *fiberpkg.Error
	if !errors.As(err, &fiberErr) {
		return false
	}
	return fiberErr.Code == fiberpkg.StatusNotFound || fiberErr.Code == fiberpkg.StatusMethodNotAllowed
}
