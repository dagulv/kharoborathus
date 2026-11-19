package domain

import (
	"errors"
	"time"
)

var (
	ErrCategoryExists  = errors.New("Category already exists")
	ErrUnknownCategory = errors.New("Unknown category")
)

type Category struct {
	Key       string            `json:"key"`
	Enabled   bool              `json:"enabled"`
	Terms     map[string]string `json:"terms"` // lang to term
	Tags      []string          `json:"tags"`
	CreatedAt time.Time         `json:"createdAt"`
}

type AdminCategory struct {
	Category

	TagCount  int `json:"tagCount"`
	PostCount int `json:"postCount"`
}

type NewCategory struct {
	Key string `json:"key"`
	Sv  string `json:"sv"`
	En  string `json:"en"`
}
