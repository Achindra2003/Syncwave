// Package ai provides an LLM-powered text completion assistant using
// the Groq API (OpenAI-compatible). It streams completion tokens as
// they are generated, allowing real-time ghost-text suggestions in the editor.
package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

// Assistant wraps the LLM client and provides streaming text completion.
type Assistant struct {
	llm        *openai.LLM
	model      string
	maxContext int
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
		maxContext: 500,
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

		prompt := a.buildPrompt(textBefore, textAfter)

		_, err := a.llm.GenerateContent(ctx,
			[]llms.MessageContent{
				llms.TextParts(llms.ChatMessageTypeHuman, prompt),
			},
			llms.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
				resultChan <- StreamResult{Token: string(chunk)}
				return nil
			}),
		)

		if err != nil {
			resultChan <- StreamResult{Error: err}
		}
	}()

	return resultChan
}

func (a *Assistant) buildPrompt(textBefore, textAfter string) string {
	afterCtx := ""
	if len(textAfter) > 0 {
		if len(textAfter) > 200 {
			textAfter = textAfter[:200]
		}
		afterCtx = fmt.Sprintf("\n\nText AFTER the cursor (for context only — do NOT repeat this):\n\"\"\"%s\"\"\"", textAfter)
	}

	return fmt.Sprintf(`You are an inline autocomplete assistant for a collaborative document editor. Insert text at the cursor position.

Rules:
- Output ONLY the words/phrases to insert at the cursor. Nothing else.
- Keep it natural and contextually appropriate (use both before and after context).
- Maximum 30 words. Prefer short, useful completions (5-15 words).
- Do NOT repeat any text that already exists before or after the cursor.
- If mid-sentence, complete the sentence naturally.
- If between sentences, suggest a connecting sentence.
- Match the tone, style, and language of the surrounding text.

Text BEFORE the cursor:
"""%s"""%s

Insert at cursor:`, textBefore, afterCtx)
}
