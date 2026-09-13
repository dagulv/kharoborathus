package feed

import (
	"strconv"
	"strings"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

var orderColumns = map[string]string{
	"":          "created_at",
	"id":        "id",
	"title":     "title",
	"content":   "content",
	"createdAt": "created_at",
}

type Filter struct {
	Limit   string `query:"limit"`
	Offset  string `query:"offset"`
	Order   string `query:"order"`
	OrderBy string `query:"orderBy"`
	Search  string `query:"search"`
}

func (f Filter) limit() int {
	if f.Limit == "" {
		return defaultLimit
	}

	limit, err := strconv.Atoi(f.Limit)
	if err != nil {
		return defaultLimit
	}

	if limit < 1 {
		return defaultLimit
	}

	return min(limit, maxLimit)
}

func (f Filter) offset() int {
	if f.Offset == "" {
		return 0
	}

	offset, err := strconv.Atoi(f.Offset)
	if err != nil {
		return 0
	}

	if offset < 0 {
		return 0
	}

	return offset
}

func (f Filter) sort() (column string, direction string) {
	column, ok := orderColumns[f.OrderBy]
	if !ok {
		return "id", "DESC"
	}

	switch strings.ToLower(f.Order) {
	case "", "desc":
		direction = "DESC"
	case "asc":
		direction = "ASC"
	default:
		return "id", "DESC"
	}

	return column, direction
}

type Feed struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}
