package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strings"
)

type DefaultCorpusReport struct {
	DefaultCorpus  string `json:"defaultCorpus"`
	IndexDirectory string `json:"indexDirectory"`
}

func runSetDefault(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("set-default", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "exact attached corpus name")
	indexDir := fs.String("index", "", "directory containing the Zoekt index")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("set-default does not accept positional arguments")
	}
	if strings.TrimSpace(*name) == "" {
		return errors.New("set-default: --name is required")
	}
	resolved, err := resolveIndexDir(*indexDir)
	if err != nil {
		return err
	}
	if err := requireIndex(resolved); err != nil {
		return err
	}
	canonical, err := setDefaultCorpus(resolved, *name)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(DefaultCorpusReport{
		DefaultCorpus:  canonical,
		IndexDirectory: resolved,
	})
}
