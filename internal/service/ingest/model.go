package ingest

type ScanPost struct {
	ID      string
	Content string
	BaseURL string
}

const (
	statusAccepted  = "accepted"
	statusRejected  = "rejected"
	statusDuplicate = "duplicate"
	statusFailed    = "failed"
)

type Link struct {
	ID         string
	URL        string
	Via        string
	FromPostID string
}
