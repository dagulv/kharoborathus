package adapter

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/dagulv/kharoborathus/internal/lib/config"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/invopop/jsonschema"
)

const (
	baseURL = "https://api.deepseek.com/anthropic"

	ModelFlash = "deepseek-flash"

	maxTokens      = 4096 // models generate at most 384K
	outputTool     = "output"
	requestTimeout = 5 * time.Minute
)

var _ LLM = deepSeek{}

//go:embed prompts/output_tool.md
var outputToolPrompt string

var outputToolDescription = strings.TrimSpace(outputToolPrompt)

type deepSeek struct {
	client anthropic.Client
	l      *slog.Logger
}

func NewDeepSeek(env *config.Environment, l *slog.Logger) (LLM, error) {
	if env.DeepseekAPIKey == "" {
		return nil, errors.New("deepseek: no api key")
	}

	client := anthropic.NewClient(
		option.WithAPIKey(env.DeepseekAPIKey),
		option.WithBaseURL(baseURL),
		option.WithMaxRetries(3),
		option.WithRequestTimeout(requestTimeout),
	)

	return deepSeek{
		client: client,
		l:      l,
	}, nil
}

func (d deepSeek) Prompt(ctx context.Context, req Request) (_ string, err error) {
	params, err := d.params(req)

	if err != nil {
		return
	}

	msg, err := d.send(ctx, params)

	if err != nil {
		return
	}

	var text strings.Builder

	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}

	return text.String(), nil
}

func (d deepSeek) Unmarshal(ctx context.Context, req Request, out any) (err error) {
	params, err := d.params(req)

	if err != nil {
		return
	}

	schema, err := schemaOf(out)

	if err != nil {
		return
	}

	tool := anthropic.ToolUnionParamOfTool(schema, outputTool)
	tool.OfTool.Description = anthropic.String(outputToolDescription)

	params.Tools = []anthropic.ToolUnionParam{tool}
	params.ToolChoice = anthropic.ToolChoiceParamOfTool(outputTool)

	msg, err := d.send(ctx, params)

	if err != nil {
		return
	}

	for _, block := range msg.Content {
		use, ok := block.AsAny().(anthropic.ToolUseBlock)

		if !ok || use.Name != outputTool {
			continue
		}

		return json.Unmarshal(use.Input, out)
	}

	return fmt.Errorf("deepseek: no %s call in response", outputTool)
}

func (d deepSeek) send(ctx context.Context, params anthropic.MessageNewParams) (msg *anthropic.Message, err error) {
	if err = d.gate(); err != nil {
		return
	}

	msg, err = d.client.Messages.New(ctx, params)

	if err != nil {
		return nil, apiError(err)
	}

	d.l.DebugContext(ctx, "deepseek completion",
		"model", string(msg.Model),
		"stopReason", string(msg.StopReason),
		"inputTokens", msg.Usage.InputTokens,
		"cachedTokens", msg.Usage.CacheReadInputTokens,
		"outputTokens", msg.Usage.OutputTokens,
	)

	if msg.StopReason == anthropic.StopReasonMaxTokens {
		return nil, fmt.Errorf("%w: deepseek reached max_tokens", ErrIncomplete)
	}

	return msg, nil
}

func (d deepSeek) params(req Request) (params anthropic.MessageNewParams, err error) {
	if req.Prompt == "" {
		return params, errors.New("deepseek: no prompt")
	}

	params = anthropic.MessageNewParams{
		Model:     anthropic.Model(cmp.Or(req.Model, ModelFlash)),
		MaxTokens: maxTokens,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(req.Prompt))},
	}

	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}

	if req.Temperature != nil {
		params.Temperature = anthropic.Float(*req.Temperature)
	}

	return params, nil
}

var reflector = jsonschema.Reflector{
	Anonymous:      true,
	DoNotReference: true,
	ExpandedStruct: true,
}

func schemaOf(out any) (anthropic.ToolInputSchemaParam, error) {
	v := reflect.ValueOf(out)

	if v.Kind() != reflect.Pointer || v.IsNil() {
		return anthropic.ToolInputSchemaParam{}, errors.New("deepseek: out must be a non nil pointer")
	}

	s := reflector.ReflectFromType(v.Type().Elem())

	if s.Properties == nil {
		return anthropic.ToolInputSchemaParam{}, errors.New("deepseek: out must point at a struct")
	}

	return anthropic.ToolInputSchemaParam{
		Properties: s.Properties,
		Required:   s.Required,
	}, nil
}

func apiError(err error) error {
	var apiErr *anthropic.Error

	if !errors.As(err, &apiErr) {
		return err
	}

	switch apiErr.StatusCode {
	case http.StatusPaymentRequired, http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	return err
}

// Off peak hours

// beijing is where the peak windows and the holiday calendar are anchored. Fixed
// rather than loaded, since china has no dst and the image carries no tzdata.
var beijing = time.FixedZone("CST", 8*60*60)

var OffPeak = schedule.Schedule{
	Closed: []schedule.Window{
		{Days: schedule.Weekdays, From: 1 * time.Hour, To: 4 * time.Hour},
		{Days: schedule.Weekdays, From: 6 * time.Hour, To: 10 * time.Hour},
	},
	// The closed windows cover 09:00-18:00 in beijing, so a holiday is looked up
	// on the chinese calendar day rather than the UTC one.
	Open: func(t time.Time) bool {
		_, ok := chineseHolidays[dayKey(t.In(beijing))]

		return ok
	},
}

// chineseHolidays are the days off announced in 国务院办公厅关于2026年部分节假日安排的通知
// (2025-11-04), keyed by their date in beijing time. Extend this every year. A
// year that is missing simply counts as having no holidays, which costs the robot
// working hours but never money.
var chineseHolidays = holidays(
	holidayRange{2026, time.January, 1, 3},    // 元旦
	holidayRange{2026, time.February, 15, 9},  // 春节
	holidayRange{2026, time.April, 4, 3},      // 清明节
	holidayRange{2026, time.May, 1, 5},        // 劳动节
	holidayRange{2026, time.June, 19, 3},      // 端午节
	holidayRange{2026, time.September, 25, 3}, // 中秋节
	holidayRange{2026, time.October, 1, 7},    // 国庆节
)

type holidayRange struct {
	year  int
	month time.Month
	day   int
	days  int
}

func holidays(ranges ...holidayRange) map[int]struct{} {
	m := make(map[int]struct{})

	for _, r := range ranges {
		day := time.Date(r.year, r.month, r.day, 0, 0, 0, 0, beijing)

		for range r.days {
			m[dayKey(day)] = struct{}{}
			day = day.AddDate(0, 0, 1)
		}
	}

	return m
}

func dayKey(t time.Time) int {
	y, m, d := t.Date()

	return y*10000 + int(m)*100 + d
}

func (d deepSeek) Schedule() schedule.Schedule {
	return OffPeak
}

// gate refuses the call during peak hours. Callers back off until the schedule
// opens again, or drive their flow off Schedule().Tick.
func (d deepSeek) gate() error {
	now := time.Now()

	if OffPeak.Active(now) {
		return nil
	}

	return fmt.Errorf("%w: deepseek is in peak hours until %s", ErrUnavailable, OffPeak.Next(now).Format(time.RFC3339))
}
