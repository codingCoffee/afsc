// Package metrics holds the process counters served by expvar on AFSC_METRICS_ADDR (build plan §16). They're
// registered once per process; the metrics listener is never the public one.
package metrics

import "expvar"

var (
	// PageCache counts page cache lookups: "hit" and "miss".
	PageCache = expvar.NewMap("afsc_page_cache")
	// Jobs counts finished job runs by "<kind>.ok", "<kind>.retry" and "<kind>.failed" (out of attempts).
	Jobs = expvar.NewMap("afsc_jobs")
	// Webhooks counts delivery attempts by outcome: succeeded, retry, failed and cancelled.
	Webhooks = expvar.NewMap("afsc_webhook_deliveries")
	// PluginFailures counts failed plugin hook, func and action calls by hook name.
	PluginFailures = expvar.NewMap("afsc_plugin_failures")
	// Requests counts HTTP responses by status class: 1xx … 5xx.
	Requests = expvar.NewMap("afsc_requests")
)
