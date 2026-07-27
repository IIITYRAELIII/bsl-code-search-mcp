package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type IndexReport struct {
	Repository     string   `json:"repository"`
	Source         string   `json:"source"`
	IndexDirectory string   `json:"indexDirectory"`
	Default        bool     `json:"default"`
	Extensions     []string `json:"extensions"`
	IndexedFiles   int      `json:"indexedFiles"`
	IndexedBytes   int64    `json:"indexedBytes"`
	DurationMillis int64    `json:"durationMillis"`
}

func runIndex(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "stable name for this configuration or source dump")
	source := fs.String("source", "", "configuration source directory to index")
	indexDir := fs.String("index", "", "directory containing the Zoekt index")
	extensions := fs.String("extensions", "bsl", "comma-separated extensions to index")
	zoektBin := fs.String("zoekt-bin", "", "directory containing zoekt-index")
	makeDefault := fs.Bool("default", false, "make this corpus the default search scope")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("index does not accept positional arguments")
	}
	if strings.TrimSpace(*name) == "" {
		return errors.New("index: --name is required")
	}
	if err := validateCorpusName(*name); err != nil {
		return err
	}
	if strings.TrimSpace(*source) == "" {
		return errors.New("index: --source is required")
	}

	resolvedIndex, err := resolveIndexDir(*indexDir)
	if err != nil {
		return err
	}
	resolvedSource, err := filepath.Abs(filepath.Clean(*source))
	if err != nil {
		return fmt.Errorf("resolve source directory: %w", err)
	}
	info, err := os.Stat(resolvedSource)
	if err != nil {
		return fmt.Errorf("inspect source directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source is not a directory: %s", resolvedSource)
	}
	if err := os.MkdirAll(resolvedIndex, 0o755); err != nil {
		return fmt.Errorf("create index directory: %w", err)
	}

	exts, err := parseExtensions(*extensions)
	if err != nil {
		return err
	}
	report, err := buildIndex(
		*name,
		resolvedSource,
		resolvedIndex,
		exts,
		*zoektBin,
		*makeDefault,
		stderr,
	)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func validateCorpusName(name string) error {
	if name == "" {
		return errors.New("index: --name must not be empty")
	}
	if len([]rune(name)) > 100 {
		return errors.New("index: --name must not exceed 100 characters")
	}
	if strings.TrimSpace(name) != name || strings.HasSuffix(name, ".") {
		return errors.New("index: --name must not have leading/trailing spaces or a trailing dot")
	}
	if strings.ContainsAny(name, `\/:*?"<>|`) {
		return errors.New(`index: --name must not contain \ / : * ? " < > or |`)
	}
	return nil
}

func parseExtensions(value string) ([]string, error) {
	seen := make(map[string]struct{})
	var result []string
	for _, raw := range strings.Split(value, ",") {
		ext := strings.ToLower(strings.TrimSpace(raw))
		ext = strings.TrimPrefix(ext, ".")
		if ext == "" {
			continue
		}
		if strings.ContainsAny(ext, `\/:*?"<>|`) {
			return nil, fmt.Errorf("invalid extension %q", raw)
		}
		if _, ok := seen[ext]; !ok {
			seen[ext] = struct{}{}
			result = append(result, ext)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("at least one extension is required")
	}
	sort.Strings(result)
	return result, nil
}

func buildIndex(
	name, source, indexDir string,
	extensions []string,
	zoektBin string,
	makeDefault bool,
	stderr io.Writer,
) (*IndexReport, error) {
	started := time.Now()
	executable, err := resolveZoektExecutable(zoektBin, "zoekt-index")
	if err != nil {
		return nil, err
	}
	report := &IndexReport{
		Repository:     name,
		Source:         source,
		IndexDirectory: indexDir,
		Default:        makeDefault,
		Extensions:     extensions,
	}
	report.IndexedFiles, report.IndexedBytes, err = scanSource(source, extensions)
	if err != nil {
		return nil, fmt.Errorf("scan selected source: %w", err)
	}
	if report.IndexedFiles == 0 {
		return nil, fmt.Errorf("no files with extensions %s found under %s", strings.Join(extensions, ","), source)
	}
	command := newBackendCommand(
		executable,
		"-disable_ctags",
		"-include_ext", strings.Join(extensions, ","),
		"-index", indexDir,
		"-name", name,
		source,
	)
	command.Stdout = stderr
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("zoekt-index failed: %w", err)
	}
	if err := updateManifest(indexDir, manifestCorpus{
		Name:        name,
		Source:      source,
		Extensions:  extensions,
		Files:       report.IndexedFiles,
		SourceBytes: report.IndexedBytes,
		IndexedAt:   time.Now(),
	}, makeDefault); err != nil {
		return nil, err
	}
	manifest, err := loadManifest(indexDir)
	if err != nil {
		return nil, err
	}
	report.Default = strings.EqualFold(name, effectiveDefaultCorpus(manifest))
	report.DurationMillis = time.Since(started).Milliseconds()
	return report, nil
}
