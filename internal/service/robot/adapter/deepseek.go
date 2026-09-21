package adapter

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	baseURL = "https://api.deepseek.com/anthropic"
)

type deepSeek struct {
	client anthropic.Client
}

func NewDeepSeek(env *config.Env) LLM {
	anthropic.NewClient(
		option.WithAPIKey()
	)

	return deepSeek{}
}
