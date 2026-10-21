package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/cjonesy/git-ai-commit/internal/config"
	"github.com/cjonesy/git-ai-commit/internal/git"
	"github.com/cjonesy/git-ai-commit/internal/llm"
	"github.com/cjonesy/git-ai-commit/internal/message"
	"github.com/cjonesy/git-ai-commit/internal/prompt"
)

// writer asks the model for a commit message.
type writer struct {
	cfg    *config.Config
	client *llm.Client
}

// draft is a generated message and, if it's still invalid after one retry, why.
type draft struct {
	text    string
	problem error
}

// write generates a message for files. A diff over the budget is first
// summarized file by file; an invalid message gets one retry.
func (w *writer) write(ctx context.Context, files []git.File, branch, hint string) (draft, error) {
	// budget is in characters (roughly 4 per token)
	budget := w.cfg.MaxDiffChars
	overview := make([]string, len(files))
	var diff strings.Builder
	for i, f := range files {
		overview[i] = f.String()
		diff.WriteString(f.Patch)
	}
	req := prompt.Request{
		Overview: prompt.FitLines(overview, budget),
		Branch:   branch,
		Hint:     hint,
		Diff:     diff.String(),
	}
	if utf8.RuneCountInString(req.Diff) > budget {
		summaries, err := w.summarize(ctx, files)
		if err != nil {
			return draft{}, err
		}
		req.Diff, req.Summarized = summaries, true
	}

	chat := []llm.Message{
		{Role: llm.RoleSystem, Content: prompt.System(w.cfg.Types, w.cfg.CommentPrefix)},
		{Role: llm.RoleUser, Content: prompt.User(req)},
	}
	reply, err := w.client.Chat(ctx, chat)
	if err != nil {
		return draft{}, err
	}
	d := w.check(reply)
	if d.problem == nil {
		return d, nil
	}

	chat = append(chat,
		llm.Message{Role: llm.RoleAssistant, Content: reply},
		llm.Message{Role: llm.RoleUser, Content: prompt.Retry(d.problem)},
	)
	if reply, err = w.client.Chat(ctx, chat); err != nil {
		return draft{}, err
	}
	return w.check(reply), nil
}

func (w *writer) check(reply string) draft {
	text := message.Normalize(reply)
	return draft{text: text, problem: message.Validate(text, w.cfg.Types, w.cfg.CommentPrefix)}
}

// summarize asks for a short summary of each file whose content is included.
func (w *writer) summarize(ctx context.Context, files []git.File) (string, error) {
	var included []git.File
	for _, f := range files {
		if f.Patch != "" {
			included = append(included, f)
		}
	}
	summaries := make([]prompt.Summary, 0, len(included))
	for i, f := range included {
		fmt.Fprintf(os.Stderr, "Summarizing %d/%d: %s\n", i+1, len(included), f.Path)
		system, user := prompt.Summarize(f.Path, prompt.Truncate(f.Patch, w.cfg.MaxDiffChars))
		text, err := w.client.Chat(ctx, []llm.Message{
			{Role: llm.RoleSystem, Content: system},
			{Role: llm.RoleUser, Content: user},
		})
		if err != nil {
			return "", err
		}
		summaries = append(summaries, prompt.Summary{Path: f.Path, Text: text})
	}
	return prompt.FitSummaries(summaries, w.cfg.MaxDiffChars), nil
}
