package zerolog

import (
	"github.com/astraive/loza/sdks/go"
	"github.com/rs/zerolog"
)

// AdapterHook routes zerolog events into loza immediate logs.
type AdapterHook struct{}

// Hook creates a zerolog hook adapter.
func Hook() AdapterHook { return AdapterHook{} }

// Run implements zerolog.Hook.
func (AdapterHook) Run(e *zerolog.Event, level zerolog.Level, msg string) {
	ctx := e.GetCtx()
	switch level {
	case zerolog.DebugLevel, zerolog.TraceLevel:
		loza.DebugContext(ctx, msg, "zerolog.event")
	case zerolog.InfoLevel:
		loza.InfoContext(ctx, msg, "zerolog.event")
	case zerolog.WarnLevel:
		loza.WarnContext(ctx, msg, "zerolog.event")
	case zerolog.ErrorLevel, zerolog.FatalLevel, zerolog.PanicLevel:
		loza.ErrorContext(ctx, msg, nil, "zerolog.event")
	case zerolog.NoLevel:
		// NoLevel (logger.Log()/Send()) is a level-agnostic event, not an error.
		loza.InfoContext(ctx, msg, "zerolog.event")
	default:
		// Unknown/custom numeric levels are not necessarily errors either;
		// emit them at a neutral level rather than mislabeling them.
		loza.InfoContext(ctx, msg, "zerolog.event")
	}
}
