package ai

import (
	"context"
	"strings"
	"testing"
)

func TestSendStreamResultSendsWhenContextActive(t *testing.T) {
	ctx := context.Background()
	ch := make(chan StreamResult, 1)
	result := StreamResult{Token: "ok"}

	if sent := sendStreamResult(ctx, ch, result); !sent {
		t.Fatalf("expected send to succeed with active context")
	}

	got := <-ch
	if got.Token != "ok" {
		t.Fatalf("unexpected token: got %q want %q", got.Token, "ok")
	}
}

func TestSendStreamResultSkipsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := make(chan StreamResult, 1)
	result := StreamResult{Token: "x"}

	if sent := sendStreamResult(ctx, ch, result); sent {
		t.Fatalf("expected send to fail when context is canceled")
	}
	if len(ch) != 0 {
		t.Fatalf("expected channel to remain empty after canceled send")
	}
}

func TestTrimTailRunes(t *testing.T) {
	got := trimTailRunes("abcdef", 3)
	if got != "def" {
		t.Fatalf("trimTailRunes: got %q want %q", got, "def")
	}
}

func TestTrimHeadRunes(t *testing.T) {
	got := trimHeadRunes("abcdef", 2)
	if got != "ab" {
		t.Fatalf("trimHeadRunes: got %q want %q", got, "ab")
	}
}

func TestBuildPromptIncludesCursorAndRightContext(t *testing.T) {
	a := &Assistant{maxContext: 1600, maxAfter: 700}
	prompt := a.buildPrompt("The quick brown ", "fox jumps")

	if !strings.Contains(prompt, "[CURSOR]") {
		t.Fatalf("expected prompt to include [CURSOR] marker")
	}
	if !strings.Contains(prompt, "fox jumps") {
		t.Fatalf("expected prompt to include right-side context")
	}
	if !strings.Contains(prompt, "Full surrounding context") {
		t.Fatalf("expected structured prompt sections")
	}
}
