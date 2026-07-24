package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	appName = "bsl-code-search-mcp"
)

var appVersion = "dev"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageError()
	}

	switch args[0] {
	case "serve":
		return runServe(ctx, args[1:], stderr)
	case "index":
		return runIndex(args[1:], stdout, stderr)
	case "status":
		return runStatus(ctx, args[1:], stdout)
	case "version", "--version", "-version":
		fmt.Fprintln(stdout, appVersion)
		return nil
	case "help", "--help", "-help", "-h":
		fmt.Fprint(stdout, usage())
		return nil
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage())
	}
}

func runServe(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	indexDir := fs.String("index", "", "directory containing the Zoekt index")
	zoektBin := fs.String("zoekt-bin", "", "directory containing zoekt-webserver")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("serve does not accept positional arguments")
	}

	resolved, err := resolveIndexDir(*indexDir)
	if err != nil {
		return err
	}
	if err := requireIndex(resolved); err != nil {
		return err
	}

	backend, err := StartBackend(ctx, resolved, *zoektBin, stderr)
	if err != nil {
		return err
	}
	defer backend.Close()

	service := NewSearchService(resolved, backend.Client())

	server := mcp.NewServer(
		&mcp.Implementation{Name: appName, Version: appVersion},
		nil,
	)
	registerTools(server, service)
	log.SetOutput(stderr)
	log.Printf("%s %s: index loaded from %s", appName, appVersion, resolved)
	return server.Run(ctx, &mcp.StdioTransport{})
}

func resolveIndexDir(explicit string) (string, error) {
	if explicit == "" {
		explicit = os.Getenv("BSL_CODE_SEARCH_INDEX")
	}
	if explicit == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("resolve user cache directory: %w", err)
		}
		explicit = filepath.Join(base, appName, "index")
	}
	resolved, err := filepath.Abs(filepath.Clean(explicit))
	if err != nil {
		return "", fmt.Errorf("resolve index directory: %w", err)
	}
	return resolved, nil
}

func usageError() error {
	return errors.New(usage())
}

func usage() string {
	return strings.TrimSpace(`
BSL code search backed by a local Zoekt index.

Usage:
  bsl-code-search-mcp index --name NAME --source PATH [--extensions bsl,xml]
  bsl-code-search-mcp status
  bsl-code-search-mcp serve
  bsl-code-search-mcp version

Use --index PATH to override the default local index. Use --zoekt-bin PATH when
the pinned Zoekt backend executables are not next to this program.
`) + "\n"
}
