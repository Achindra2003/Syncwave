// Package ai provides an LLM-powered text completion assistant using
// the Groq API (OpenAI-compatible). It streams completion tokens as
// they are generated, allowing real-time ghost-text suggestions in the editor.
package ai

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

// Assistant wraps the LLM client and provides streaming text completion.
type Assistant struct {
	llm        *openai.LLM
	model      string
	maxContext int
	maxAfter   int
	timeout    time.Duration
}

// StreamResult carries a single streamed token or an error.
type StreamResult struct {
	Token string
	Error error
}

// NewAssistant creates a new AI assistant connected to the Groq API.
func NewAssistant(apiKey string) (*Assistant, error) {
	llm, err := openai.New(
		openai.WithToken(apiKey),
		openai.WithBaseURL("https://api.groq.com/openai/v1"),
		openai.WithModel("llama-3.1-8b-instant"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM client: %w", err)
	}

	return &Assistant{
		llm:        llm,
		model:      "llama-3.1-8b-instant",
		maxContext: 1600,
		maxAfter:   700,
		timeout:    15 * time.Second,
	}, nil
}

// StreamComplete returns a channel of tokens that complete the text at the cursor.
// textBefore and textAfter provide context around the cursor position.
func (a *Assistant) StreamComplete(ctx context.Context, textBefore, textAfter string) <-chan StreamResult {
	resultChan := make(chan StreamResult, 100)

	go func() {
		defer close(resultChan)

		ctx, cancel := context.WithTimeout(ctx, a.timeout)
		defer cancel()

		before := trimTailRunes(textBefore, a.maxContext)
		after := trimHeadRunes(textAfter, a.maxAfter)
		prompt := a.buildPrompt(before, after)

		_, err := a.llm.GenerateContent(ctx,
			[]llms.MessageContent{
				llms.TextParts(llms.ChatMessageTypeHuman, prompt),
			},
			llms.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
				if !sendStreamResult(ctx, resultChan, StreamResult{Token: string(chunk)}) {
					return ctx.Err()
				}
				return nil
			}),
		)

		if err != nil {
			sendStreamResult(ctx, resultChan, StreamResult{Error: err})
		}
	}()

	return resultChan
}

func sendStreamResult(ctx context.Context, out chan<- StreamResult, result StreamResult) bool {
	if ctx.Err() != nil {
		return false
	}

	select {
	case <-ctx.Done():
		return false
	case out <- result:
		return true
	}
}

func (a *Assistant) buildPrompt(textBefore, textAfter string) string {
	leftLocal := tailByLineCount(textBefore, 5)
	rightLocal := headByLineCount(textAfter, 5)

	return fmt.Sprintf(`You are an inline autocomplete assistant for a collaborative document editor.
Your task: generate text to insert exactly at [CURSOR].

Rules:
- Output ONLY the inserted text, no quotes, labels, markdown, or explanations.
- Use BOTH left and right context. The completion must flow naturally into the existing right context.
- Prefer precise continuation over generic filler.
- Keep completion concise (usually 5-20 words, hard max 40 words).
- Do NOT repeat text already present around the cursor.
- If right context already starts a complete continuation, output an empty string.

Full surrounding context:
"""
%s[CURSOR]%s
"""

Immediate left context (closest lines):
"""
%s
"""

Immediate right context (closest lines):
"""
%s
"""

Insert text at [CURSOR]:`, textBefore, textAfter, leftLocal, rightLocal)
}

func trimTailRunes(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[len(runes)-maxRunes:])
}

func trimHeadRunes(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxRunes])
}

func tailByLineCount(text string, lines int) string {
	if lines <= 0 || text == "" {
		return ""
	}
	parts := strings.Split(text, "\n")
	if len(parts) <= lines {
		return text
	}
	return strings.Join(parts[len(parts)-lines:], "\n")
}

func headByLineCount(text string, lines int) string {
	if lines <= 0 || text == "" {
		return ""
	}
	parts := strings.Split(text, "\n")
	if len(parts) <= lines {
		return text
	}
	return strings.Join(parts[:lines], "\n")
}
