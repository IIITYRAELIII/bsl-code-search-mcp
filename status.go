package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
)

func runStatus(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	indexDir := fs.String("index", "", "directory containing the Zoekt index")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("status does not accept positional arguments")
	}
	resolved, err := resolveIndexDir(*indexDir)
	if err != nil {
		return err
	}
	if err := requireIndex(resolved); err != nil {
		return err
	}
	service := NewSearchService(resolved, nil)
	status, err := service.ListCorpora(ctx)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(status)
}
