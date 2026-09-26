package feed

import "time"

// Feed also stores Raw HTML Content on S3 at /feeds/<id>/content
type Feed struct {
	ID           string   `json:"id"`
	SourceID     string   `json:"sourceId"`
	URL          string   `json:"url"`
	Lang         string   `json:"lang"`
	DomainID     string   `json:"domainId"` // Used for icon/brand and link, resolved from url
	Title        string   `json:"title"`
	ShortContent string   `json:"shortContent"` // Plain text short content, llm summary of content
	Content      string   `json:"content"`      // Markdown content
	Tags         []string `json:"tags"`
	Authors      []string `json:"authors"`
	// MD5Hash      string    `json:"md5Hash"` // Maybe needed, derived from external ids, URL, other.
	PostedAt  time.Time `json:"postedAt"`
	CreatedAt time.Time `json:"createdAt"`
}
