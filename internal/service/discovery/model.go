package discovery

import "time"

type DiscoverItem struct {
	ID         string     `json:"id"`
	PostID     string     `json:"postId"`
	Entity     string     `json:"entity"`
	Action     string     `json:"action"`
	Query      string     `json:"query"`
	SearchedAt *time.Time `json:"searchedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

type Post struct {
	ID      string
	Content string
}
