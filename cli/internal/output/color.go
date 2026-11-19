package output

import (
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

var NoColor = os.Getenv("NO_COLOR") != ""

const (
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorCyan   = "\033[36m"
	ColorBold   = "\033[1m"
	ColorDim    = "\033[2m"
	ColorReset  = "\033[0m"
)

// Colorize wraps text in the given terminal color escape sequence. Control
// bytes in text are stripped first so that untrusted strings (for example
// graph labels derived from external data) cannot inject ANSI/OSC sequences
// such as OSC 8 hyperlinks or OSC 52 clipboard writes into the user's
// terminal. Sanitizing also applies when color output is disabled, since raw
// control bytes are dangerous even without an escape sequence wrapper.
func Colorize(color, text string) string {
	safe := stripControlBytes(text)
	if NoColor {
		return safe
	}
	return color + safe + ColorReset
}

// stripControlBytes removes all C0 (0x00-0x1F) and C1 (0x7F-0x9F) control
// characters, including ESC (0x1B) which introduces ANSI/CSI/OSC sequences.
// It operates on decoded runes so valid multi-byte UTF-8 text is preserved,
// while raw/invalid bytes (such as an 8-bit C1 control like 0x9D) are dropped.
func stripControlBytes(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			// Invalid UTF-8 byte; this includes raw 8-bit C1 controls.
			i += size
			continue
		}
		if unicode.IsControl(r) {
			i += size
			continue
		}
		out.WriteRune(r)
		i += size
	}
	return out.String()
}

func Success(text string) string { return Colorize(ColorGreen, text) }
func Error(text string) string   { return Colorize(ColorRed, text) }
func Warning(text string) string { return Colorize(ColorYellow, text) }
func Info(text string) string    { return Colorize(ColorCyan, text) }
func Bold(text string) string    { return Colorize(ColorBold, text) }
func Dim(text string) string     { return Colorize(ColorDim, text) }

func SuccessLabel() string { return Success("ok") }
func ErrorLabel() string   { return Error("error") }

func PrintSection(title string) {
	fmt.Println(Bold(title))
}
