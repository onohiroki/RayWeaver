// Command rayweave-mcp exposes RayWeaver to AI agents over the Model Context
// Protocol (stdio transport).
//
// It is a protocol adapter, not a second optics engine: every tool call runs the
// rayweave CLI with a pipeline document on stdin and turns its separate
// stdout/stderr into a document handle, a compact summary and warnings. Long
// global searches (escape, pso) are started detached, exactly like rwrun, so
// they survive an MCP server restart and are stopped with SIGTERM twice.
//
// Point a client at the binary:
//
//	{"mcp":{"rayweave":{"type":"local","command":["/path/to/rayweave-mcp"]}}}
//
// Manual: docs/mcp.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hiroki/rayweaver/internal/mcpsrv"
	"github.com/hiroki/rayweaver/internal/runctl"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is set at build time with -ldflags '-X main.Version=...'.
var Version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "rayweave-mcp: %v\n", err)
		os.Exit(1)
	}
}

func run(argv []string) error {
	fs := flag.NewFlagSet("rayweave-mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { usage(fs) }

	var (
		rayweave  = fs.String("rayweave", "", "rayweave binary (default: $RAYWEAVE, ./rayweave, $PATH, then beside this binary)")
		workspace = fs.String("workspace", "", "workspace for documents, jobs and plots (default: $RAYWEAVE_WORKSPACE, else ./.rw-mcp)")
		glassDir  = fs.String("glass-dir", "", "default AGF glass catalog directory (default: $RAYWEAVE_GLASS_DIR)")
		tools     = fs.String("tools", "", "tool set: core (default) or all")
		maxInline = fs.Int("max-inline-bytes", 0, "maximum inline document size (default 2 MiB)")
		maxJobs   = fs.Int("max-jobs", 0, "maximum concurrent escape/pso jobs (default 2)")
		version   = fs.Bool("version", false, "print the version and exit")
	)
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if *version {
		fmt.Printf("rayweave-mcp %s\n", Version)
		return nil
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (this server speaks MCP on stdin/stdout; see -h)", fs.Arg(0))
	}

	cfg := mcpsrv.DefaultConfig()
	cfg.Version = Version
	if v := firstNonEmpty(*tools, os.Getenv("RAYWEAVE_MCP_TOOLS")); v != "" {
		cfg.Tools = v
	}
	if v := firstNonEmpty(*workspace, os.Getenv("RAYWEAVE_WORKSPACE")); v != "" {
		cfg.Workspace = v
	}
	if v := firstNonEmpty(*glassDir, os.Getenv("RAYWEAVE_GLASS_DIR")); v != "" {
		cfg.GlassDir = v
	}
	if *maxInline > 0 {
		cfg.MaxInlineBytes = *maxInline
	}
	if *maxJobs > 0 {
		cfg.MaxJobs = *maxJobs
	}
	if cfg.Tools != "core" && cfg.Tools != "all" {
		return fmt.Errorf("-tools must be core or all, got %q", cfg.Tools)
	}

	bin := *rayweave
	if bin == "" {
		resolved, err := runctl.Resolve("")
		if err != nil {
			return err
		}
		bin = resolved
	}
	cfg.Binary = bin

	srv, err := mcpsrv.New(cfg)
	if err != nil {
		return err
	}

	// Diagnostics never touch stdout: it carries the JSON-RPC stream.
	fmt.Fprintf(os.Stderr, "rayweave-mcp %s: %s, workspace %s, tools %s\n",
		Version, cfg.Binary, runctl.AbsPath(cfg.Workspace), cfg.Tools)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func usage(fs *flag.FlagSet) {
	fmt.Fprint(fs.Output(), `rayweave-mcp - RayWeaver over the Model Context Protocol (stdio)

Usage:
  rayweave-mcp [options]

Options:
  -rayweave PATH          rayweave binary (default: $RAYWEAVE, ./rayweave, $PATH,
                          then beside this binary)
  -workspace DIR          documents, jobs and plots (default: $RAYWEAVE_WORKSPACE,
                          else ./.rw-mcp)
  -glass-dir DIR          default AGF glass catalog directory
                          (default: $RAYWEAVE_GLASS_DIR)
  -tools core|all         tool set (default: $RAYWEAVE_MCP_TOOLS, else core)
  -max-inline-bytes N     maximum inline document argument (default 2 MiB)
  -max-jobs N             maximum concurrent escape/pso jobs (default 2)
  -version                print the version and exit

The server speaks MCP on stdin/stdout and logs to stderr. It never prints a
pipeline document on its own stdout. Manual: docs/mcp.md.
`)
}
