package feed

type Filter struct {
	Limit   string `query:"limit"`
	Offset  string `query:"offset"`
	Order   string `query:"order"`
	OrderBy string `query:"orderBy"`
	Search  string `query:"search"`
}

type Feed struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}
