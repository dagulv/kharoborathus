package tagging

type Post struct {
	ID      string
	Lang    string
	Title   string
	Content string
}

type Term struct {
	Key  string
	Lang string
	Term string
}
