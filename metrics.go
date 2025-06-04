package multipartHTTP

import (
	"go.k6.io/k6/metrics"
)

//nolint:nolintlint // unfortunately it is having a false possitive
//nolint:revive
const (
	HTTPMultipartSessionsName         = "http_multipart_sessions"
	HTTPMultipartMessagesReceivedName = "http_multipart_msgs_received"
	HTTPMultipartSessionDurationName  = "http_multipart_session_duration"
)

// BuiltinMetrics represent all the builtin metrics of k6
type HTTPMultipartMetrics struct {
	// Websocket-related
	HTTPMultipartSessions         *metrics.Metric
	HTTPMultipartMessagesReceived *metrics.Metric
	HTTPMultipartSessionDuration  *metrics.Metric
}

// RegisterMetrics register and returns the builtin metrics in the provided registry
func RegisterMetrics(registry *metrics.Registry) *HTTPMultipartMetrics {
	return &HTTPMultipartMetrics{
		HTTPMultipartSessions:         registry.MustNewMetric(HTTPMultipartSessionsName, metrics.Counter),
		HTTPMultipartMessagesReceived: registry.MustNewMetric(HTTPMultipartMessagesReceivedName, metrics.Counter),
		HTTPMultipartSessionDuration:  registry.MustNewMetric(HTTPMultipartSessionDurationName, metrics.Trend, metrics.Time),
	}
}
