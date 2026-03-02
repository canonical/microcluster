package daemon

import (
	"bytes"
	"log/slog"
	"regexp"
	"strings"

	"github.com/canonical/microcluster/v4/microcluster/types"
)

// logFilter represents a filter for log messages caused by known addresses.
type logFilter struct {
	logger         *slog.Logger
	knownAddresses func() map[string]types.AddrPort
}

// newLogFilter returns a new instances of the log filter.
// It expects a slice of known addresses for which log messages will get filtered.
func newLogFilter(logger *slog.Logger, knownAddresses func() map[string]types.AddrPort) *logFilter {
	return &logFilter{
		logger:         logger,
		knownAddresses: knownAddresses,
	}
}

// unwantedLogRegex represents log texts whose presence will cause log messages to be filtered out.
var unwantedLogRegex = regexp.MustCompile(`^http: TLS handshake error from (.*):[0-9]+: EOF$`)

func (l logFilter) log() *slog.Logger {
	return l.logger
}

// Write filters the given log message p and checks whether or not it contains any
// of the unwanted log messages.
// If there is a match, a debug message is logged with the unwanted log message
// In all other cases the message is logged using the actual logger.
func (l logFilter) Write(p []byte) (int, error) {
	strippedLog := l.stripLog(p)
	if strippedLog == "" {
		return 0, nil
	}

	l.log().Info(strippedLog)
	return len(p), nil
}

// stripLog strips the log message to determine whether or not its unwanted.
func (l logFilter) stripLog(p []byte) string {
	// Strip the newline from the end if it exists.
	p = bytes.TrimRightFunc(p, func(r rune) bool {
		return r == '\n'
	})

	// Get the source IP address.
	match := unwantedLogRegex.FindSubmatch(p)
	var sourceIP string
	if match != nil {
		if match[1] != nil {
			// Trim off potential IPv6 brackets.
			sourceIP = strings.Trim(string(match[1]), "[]")
		}
	}

	logStr := string(p)

	// Discard the log if the source is in our list of known addresses.
	if sourceIP != "" {
		for _, knownAddress := range l.knownAddresses() {
			if knownAddress.Addr().String() == sourceIP {
				l.log().Debug("Filtered out unwanted log text", slog.String("text", logStr))
				return ""
			}
		}
	}

	return logStr
}
