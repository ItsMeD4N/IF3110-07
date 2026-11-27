package metrics

import (
	"net/http"
	"time"
)

var Default = NewRegistry()

var (
	jobsEnqueued = Default.Counter("if3110_migration_jobs_enqueued_total",
		"Jumlah job migration yang dipublikasikan ke queue.", "action")
	jobsFinished = Default.Counter("if3110_migration_jobs_finished_total",
		"Jumlah job migration yang mencapai status terminal.", "action", "status")
	jobRetries = Default.Counter("if3110_migration_job_retries_total",
		"Jumlah retry job migration yang dijadwalkan ulang setelah kegagalan sementara.", "action")
	lockContended = Default.Counter("if3110_migration_lock_contended_total",
		"Jumlah job yang dijadwalkan ulang karena lock database sedang dipegang worker lain.", "action")
	jobsInFlight = Default.Gauge("if3110_worker_jobs_in_flight",
		"Jumlah job yang sedang diproses worker pada proses ini.", "action")
	queueWait = Default.Histogram("if3110_migration_queue_wait_seconds",
		"Waktu tunggu job di queue sejak siap dijalankan hingga diklaim worker.", nil, "action")
	lockAcquire = Default.Histogram("if3110_migration_lock_wait_seconds",
		"Waktu yang dibutuhkan worker untuk memperoleh lock database target.",
		[]float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}, "result")
	jobDuration = Default.Histogram("if3110_migration_job_duration_seconds",
		"Durasi satu attempt job migration di worker.", nil, "action", "status")
	jobEndToEnd = Default.Histogram("if3110_migration_job_end_to_end_seconds",
		"Durasi job sejak dipublikasikan ke queue hingga status terminal.", nil, "action", "status")
	batchesProcessed = Default.Counter("if3110_migration_batches_processed_total",
		"Jumlah batch yang berhasil di-commit (termasuk batch revert).", "action")
	rowsProcessed = Default.Counter("if3110_migration_rows_processed_total",
		"Jumlah row yang diproses oleh batch yang berhasil di-commit.", "action")
	batchRetries = Default.Counter("if3110_migration_batch_retries_total",
		"Jumlah retry batch akibat kegagalan sementara.", "action")
	httpRequests = Default.Counter("if3110_http_requests_total",
		"Jumlah request HTTP yang dilayani.", "method", "route", "code")
	httpDuration = Default.Histogram("if3110_http_request_duration_seconds",
		"Durasi request HTTP.", []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}, "method", "route")
)

var knownActions = map[string]bool{
	"up": true, "rollback": true, "forward": true,
	"batch_up": true, "batch_resume": true, "batch_revert": true,
}

var knownJobStatuses = map[string]bool{
	"pending": true, "running": true, "retrying": true, "completed": true, "failed": true,
	"partial": true, "partially_reverted": true, "uncertain": true, "cancelled": true,
}

var knownAttemptStatuses = map[string]bool{
	"completed": true, "retrying": true, "contended": true, "failed": true,
	"partial": true, "partially_reverted": true, "uncertain": true, "cancelled": true,
}

func actionLabel(a string) string {
	if knownActions[a] {
		return a
	}
	return "other"
}

func statusLabel(s string) string {
	if knownJobStatuses[s] {
		return s
	}
	return "other"
}

func JobEnqueued(action string) {
	jobsEnqueued.Inc(actionLabel(action))
}

func JobStarted(action string, readyAt time.Time) {
	a := actionLabel(action)
	jobsInFlight.Inc(a)
	if !readyAt.IsZero() {
		queueWait.ObserveDuration(time.Since(readyAt), a)
	}
}

func JobAttemptDone(action, status string, d time.Duration) {
	a := actionLabel(action)
	jobsInFlight.Dec(a)
	if !knownAttemptStatuses[status] {
		status = "other"
	}
	jobDuration.ObserveDuration(d, a, status)
}

func JobFinished(action, status string, createdAt time.Time) {
	a := actionLabel(action)
	st := statusLabel(status)
	jobsFinished.Inc(a, st)
	if !createdAt.IsZero() {
		jobEndToEnd.ObserveDuration(time.Since(createdAt), a, st)
	}
}

func JobRetry(action string) {
	jobRetries.Inc(actionLabel(action))
}

func LockContended(action string) {
	lockContended.Inc(actionLabel(action))
}

func LockWait(result string, d time.Duration) {
	switch result {
	case "acquired", "contended", "blocked", "error":
	default:
		result = "error"
	}
	lockAcquire.ObserveDuration(d, result)
}

func BatchCommitted(action string, rows int64) {
	a := actionLabel(action)
	batchesProcessed.Inc(a)
	if rows > 0 {
		rowsProcessed.Add(float64(rows), a)
	}
}

func BatchRetry(action string) {
	batchRetries.Inc(actionLabel(action))
}

func Handler() http.Handler {
	return Default.Handler()
}
