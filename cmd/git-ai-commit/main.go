// Command git-ai-commit writes a Conventional Commits message for your
// pending changes with an LLM and opens it in your editor through git commit.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/pflag"

	"github.com/cjonesy/git-ai-commit/internal/config"
	"github.com/cjonesy/git-ai-commit/internal/git"
	"github.com/cjonesy/git-ai-commit/internal/llm"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code, err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		code = 1
	}
	os.Exit(code)
}

type options struct {
	all         bool
	hint        string
	dryRun      bool
	showVersion bool
}

func parseArgs(args []string) (options, error) {
	var opts options
	fs := pflag.NewFlagSet("git ai-commit", pflag.ContinueOnError)
	fs.BoolVarP(&opts.all, "all", "a", false, "also include unstaged changes to tracked files (like git commit -a)")
	fs.StringVarP(&opts.hint, "message", "m", "", "your intent, given to the model as context for the why (not used verbatim)")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "print the message to stdout and exit without committing")
	fs.BoolVar(&opts.showVersion, "version", false, "print the version and exit")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Generate a Conventional Commit message with an LLM\n\nUsage: git ai-commit [options]\n\nOptions:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() > 0 {
		return opts, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return opts, nil
}

// run does the work and returns the exit code: git's own when it commits.
func run(ctx context.Context, args []string) (int, error) {
	opts, err := parseArgs(args)
	if errors.Is(err, pflag.ErrHelp) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if opts.showVersion {
		fmt.Println("git-ai-commit", buildVersion())
		return 0, nil
	}

	repo, err := git.Open(ctx, ".")
	if err != nil {
		return 0, err
	}
	entries, err := repo.Config(ctx)
	if err != nil {
		return 0, err
	}
	cfg, err := config.Parse(entries)
	if err != nil {
		return 0, err
	}
	files, err := repo.Changes(ctx, opts.all, cfg.Exclude)
	if err != nil {
		return 0, err
	}
	// after Changes, so a command that prompts doesn't run when there's nothing to commit
	if err := cfg.ResolveAPIKey(ctx); err != nil {
		return 0, err
	}
	branch, err := repo.Branch(ctx)
	if err != nil {
		return 0, err
	}

	w := &writer{
		cfg: cfg,
		client: &llm.Client{
			Endpoint: cfg.Endpoint,
			Model:    cfg.Model,
			APIKey:   cfg.APIKey,
			Extra:    cfg.ExtraBody,
			HTTP:     &http.Client{Timeout: cfg.Timeout},
		},
	}
	fmt.Fprintf(os.Stderr, "Generating commit message with %s…\n", cfg.Model)
	start := time.Now()
	d, err := w.write(ctx, files, branch, opts.hint)
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(os.Stderr, "Generated in %.1fs\n", time.Since(start).Seconds())

	if d.problem != nil {
		next := "fix it in the editor"
		if opts.dryRun {
			next = "fix it before committing"
		}
		fmt.Fprintf(os.Stderr, "warning: the generated message is invalid (%v); %s\n", d.problem, next)
	}
	if opts.dryRun {
		fmt.Println(d.text)
		if d.problem != nil {
			return 1, nil
		}
		return 0, nil
	}

	code, err := repo.Commit(opts.all, d.text)
	if err == nil && code != 0 {
		fmt.Fprintf(os.Stderr, "The generated message is kept in %s\n", repo.MessageFile())
	}
	return code, err
}
