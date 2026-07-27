package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type SearchService struct {
	indexDir string
	client   *BackendClient
}

type SearchRequest struct {
	Query        string `json:"query" jsonschema:"Zoekt lexical query without a corpus filter unless deliberately overriding the default. Start broad, then narrow with file:, case:yes or regex:"`
	Corpus       string `json:"corpus,omitempty" jsonschema:"Exact attached corpus name. For configuration-specific facts or examples, select the corpus from the task's configuration family even when its version differs. Omit for reusable implementation templates or when no relevant family is attached; that searches the participant-selected default."`
	AllCorpora   bool   `json:"allCorpora,omitempty" jsonschema:"Search every attached corpus. Use only for an explicitly cross-configuration task."`
	MaxFiles     int    `json:"maxFiles,omitempty" jsonschema:"Maximum returned files; default 20, maximum 100"`
	MaxMatches   int    `json:"maxMatches,omitempty" jsonschema:"Maximum returned line matches across files; default 100, maximum 1000"`
	ContextLines int    `json:"contextLines,omitempty" jsonschema:"Context lines before and after each match; default 2, maximum 10"`
}

type SearchResponse struct {
	Query          string       `json:"query"`
	EffectiveQuery string       `json:"effectiveQuery"`
	Scope          string       `json:"scope"`
	Corpus         string       `json:"corpus,omitempty"`
	Guidance       string       `json:"guidance"`
	DurationMillis int64        `json:"durationMillis"`
	FileCount      int          `json:"fileCount"`
	MatchCount     int          `json:"matchCount"`
	Files          []SearchFile `json:"files"`
	Stats          SearchStats  `json:"stats"`
}

type SearchFile struct {
	Repository string        `json:"repository"`
	Path       string        `json:"path"`
	Language   string        `json:"language,omitempty"`
	Score      float64       `json:"score,omitempty"`
	Matches    []SearchMatch `json:"matches"`
}

type SearchMatch struct {
	LineNumber int    `json:"lineNumber"`
	Line       string `json:"line"`
	Before     string `json:"before,omitempty"`
	After      string `json:"after,omitempty"`
}

type SearchStats struct {
	FilesConsidered  int `json:"filesConsidered"`
	FilesLoaded      int `json:"filesLoaded"`
	ShardsScanned    int `json:"shardsScanned"`
	MatchCount       int `json:"matchCount"`
	FilesWithMatches int `json:"filesWithMatches"`
}

type CorpusListResponse struct {
	IndexDirectory string       `json:"indexDirectory"`
	DefaultCorpus  string       `json:"defaultCorpus"`
	ScopeGuidance  string       `json:"scopeGuidance"`
	Corpora        []CorpusInfo `json:"corpora"`
	Total          CorpusTotals `json:"total"`
	QueryExamples  []string     `json:"queryExamples"`
}

type CorpusInfo struct {
	Name        string   `json:"name"`
	Default     bool     `json:"default"`
	Extensions  []string `json:"extensions"`
	SourceFiles int      `json:"sourceFiles"`
	SourceBytes int64    `json:"sourceBytes"`
	IndexedAt   string   `json:"indexedAt"`
}

type CorpusTotals struct {
	Repositories int   `json:"repositories"`
	IndexBytes   int64 `json:"indexBytes"`
	Shards       int   `json:"shards"`
	SourceFiles  int   `json:"sourceFiles"`
	SourceBytes  int64 `json:"sourceBytes"`
}

type BackendClient struct {
	baseURL string
	http    *http.Client
}

type backendSearchRequest struct {
	Q    string
	Opts backendSearchOptions
}

type backendSearchOptions struct {
	TotalMaxMatchCount   int
	MaxDocDisplayCount   int
	MaxMatchDisplayCount int
	NumContextLines      int
}

type backendSearchReply struct {
	Result struct {
		FileCount       int
		FilesConsidered int
		FilesLoaded     int
		ShardsScanned   int
		MatchCount      int
		Files           []backendFileMatch
	}
}

type backendFileMatch struct {
	FileName    string
	Repository  string
	Language    string
	Score       float64
	LineMatches []struct {
		Line       []byte
		LineNumber int
		Before     []byte
		After      []byte
	}
}

var repositoryFilterPattern = regexp.MustCompile(`(?i)(^|[\s(])repo:`)

func NewSearchService(indexDir string, client *BackendClient) *SearchService {
	return &SearchService{indexDir: indexDir, client: client}
}

func (s *SearchService) Search(ctx context.Context, input SearchRequest) (*SearchResponse, error) {
	raw := strings.TrimSpace(input.Query)
	if raw == "" {
		return nil, errors.New("query must not be empty")
	}
	maxFiles, err := bounded(input.MaxFiles, 20, 100, "maxFiles")
	if err != nil {
		return nil, err
	}
	maxMatches, err := bounded(input.MaxMatches, 100, 1000, "maxMatches")
	if err != nil {
		return nil, err
	}
	contextLines, err := bounded(input.ContextLines, 2, 10, "contextLines")
	if err != nil {
		return nil, err
	}
	manifest := &indexManifest{SchemaVersion: 1}
	if !repositoryFilterPattern.MatchString(raw) && !input.AllCorpora {
		manifest, err = loadManifest(s.indexDir)
		if err != nil {
			return nil, err
		}
	}
	effectiveQuery, scope, corpus, err := scopeQuery(manifest, raw, input)
	if err != nil {
		return nil, err
	}

	started := time.Now()
	var reply backendSearchReply
	err = s.client.post(ctx, "/api/search", backendSearchRequest{
		Q: effectiveQuery,
		Opts: backendSearchOptions{
			TotalMaxMatchCount:   maxMatches,
			MaxDocDisplayCount:   maxFiles,
			MaxMatchDisplayCount: maxMatches,
			NumContextLines:      contextLines,
		},
	}, &reply)
	if err != nil {
		return nil, err
	}

	response := &SearchResponse{
		Query:          raw,
		EffectiveQuery: effectiveQuery,
		Scope:          scope,
		Corpus:         corpus,
		Guidance:       guidanceForScope(scope),
		DurationMillis: time.Since(started).Milliseconds(),
		Files:          make([]SearchFile, 0),
		Stats: SearchStats{
			FilesConsidered:  reply.Result.FilesConsidered,
			FilesLoaded:      reply.Result.FilesLoaded,
			ShardsScanned:    reply.Result.ShardsScanned,
			MatchCount:       reply.Result.MatchCount,
			FilesWithMatches: reply.Result.FileCount,
		},
	}
	for _, file := range reply.Result.Files {
		out := SearchFile{
			Repository: file.Repository,
			Path:       filepath.ToSlash(file.FileName),
			Language:   file.Language,
			Score:      file.Score,
		}
		for _, match := range file.LineMatches {
			out.Matches = append(out.Matches, SearchMatch{
				LineNumber: match.LineNumber,
				Line:       strings.TrimRight(string(match.Line), "\r\n"),
				Before:     strings.TrimRight(string(match.Before), "\r\n"),
				After:      strings.TrimRight(string(match.After), "\r\n"),
			})
			response.MatchCount++
		}
		response.Files = append(response.Files, out)
	}
	response.FileCount = len(response.Files)
	return response, nil
}

func (s *SearchService) ListCorpora(ctx context.Context) (*CorpusListResponse, error) {
	_ = ctx
	manifest, err := loadManifest(s.indexDir)
	if err != nil {
		return nil, err
	}
	shards, indexBytes, err := indexDiskStats(s.indexDir)
	if err != nil {
		return nil, err
	}
	response := &CorpusListResponse{
		IndexDirectory: s.indexDir,
		DefaultCorpus:  effectiveDefaultCorpus(manifest),
		ScopeGuidance: "For configuration-specific facts and examples, select the attached corpus from the task's configuration family; its version need not match exactly. " +
			"For reusable implementation templates or a 'do something similar' search, prefer the default corpus. " +
			"If no relevant family is attached, fall back to the default and disclose that limitation.",
		Corpora: make([]CorpusInfo, 0),
		QueryExamples: []string{
			`ДинамическийСписок`,
			`case:yes "ОписаниеТипов"`,
			`file:\.bsl$ repo:^my-config$ ДинамическийСписок`,
			`regex:"ИзменитьРеквизиты\\("`,
		},
		Total: CorpusTotals{
			Repositories: len(manifest.Corpora),
			IndexBytes:   indexBytes,
			Shards:       shards,
		},
	}
	for _, corpus := range manifest.Corpora {
		info := CorpusInfo{
			Name:        corpus.Name,
			Default:     strings.EqualFold(corpus.Name, response.DefaultCorpus),
			Extensions:  corpus.Extensions,
			SourceFiles: corpus.Files,
			SourceBytes: corpus.SourceBytes,
			IndexedAt:   corpus.IndexedAt.Format(time.RFC3339),
		}
		response.Total.SourceFiles += corpus.Files
		response.Total.SourceBytes += corpus.SourceBytes
		response.Corpora = append(response.Corpora, info)
	}
	sort.Slice(response.Corpora, func(i, j int) bool {
		if response.Corpora[i].Default != response.Corpora[j].Default {
			return response.Corpora[i].Default
		}
		return response.Corpora[i].Name < response.Corpora[j].Name
	})
	return response, nil
}

func guidanceForScope(scope string) string {
	evidence := "Lexical code-search evidence only: a zero result is not proof of absence, and found code is not an executed runtime test."
	switch scope {
	case "default":
		return "This search used the participant-selected default corpus. That is the preferred source for reusable implementation templates and 'do something similar' searches. " +
			"If the current task instead targets a known configuration family and a matching corpus is attached, rerun with corpus even when its version differs. " + evidence
	case "explicit-corpus":
		return "This search used an explicitly selected configuration corpus. For configuration-specific facts and examples, matching the task's configuration family matters more than an exact version match; verify any version-sensitive detail separately. " + evidence
	case "all-corpora":
		return "This deliberate cross-configuration search spans every attached corpus. Do not treat mixed results as belonging to the task's configuration without checking each repository. " + evidence
	case "explicit-query-filter":
		return "This search used an explicit repo: filter. Apply the same priority as corpus selection: the task's configuration family for configuration-specific facts, but the default corpus for reusable implementation templates. " + evidence
	default:
		return evidence
	}
}

func scopeQuery(
	manifest *indexManifest,
	raw string,
	input SearchRequest,
) (effective, scope, corpus string, err error) {
	hasRepositoryFilter := repositoryFilterPattern.MatchString(raw)
	requestedCorpus := strings.TrimSpace(input.Corpus)
	if requestedCorpus != input.Corpus {
		return "", "", "", errors.New("corpus must not have leading or trailing spaces")
	}
	if requestedCorpus != "" && input.AllCorpora {
		return "", "", "", errors.New("corpus and allCorpora cannot be used together")
	}
	if hasRepositoryFilter && (requestedCorpus != "" || input.AllCorpora) {
		return "", "", "", errors.New(
			"query repo: filter cannot be combined with corpus or allCorpora",
		)
	}
	if hasRepositoryFilter {
		return raw, "explicit-query-filter", "", nil
	}
	if input.AllCorpora {
		return raw, "all-corpora", "", nil
	}
	if requestedCorpus == "" {
		requestedCorpus = effectiveDefaultCorpus(manifest)
		if requestedCorpus == "" {
			return "", "", "", errors.New(
				"no default corpus is selected; run set-default --name NAME or pass corpus explicitly",
			)
		}
		scope = "default"
	} else {
		scope = "explicit-corpus"
	}
	canonical, ok := findCorpusName(manifest, requestedCorpus)
	if !ok {
		return "", "", "", fmt.Errorf("corpus %q is not attached", requestedCorpus)
	}
	return `repo:"^` + regexp.QuoteMeta(canonical) + `$" ` + raw, scope, canonical, nil
}

func (c *BackendClient) post(ctx context.Context, path string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode backend request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create backend request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("call Zoekt backend: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return fmt.Errorf("Zoekt backend returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return fmt.Errorf("decode Zoekt response: %w", err)
	}
	return nil
}

func bounded(value, defaultValue, maximum int, field string) (int, error) {
	if value == 0 {
		return defaultValue, nil
	}
	if value < 0 || value > maximum {
		return 0, fmt.Errorf("%s must be between 1 and %d", field, maximum)
	}
	return value, nil
}

func requireIndex(indexDir string) error {
	info, err := os.Stat(indexDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("index not found at %s; run %s index --name NAME --source PATH first", indexDir, appName)
		}
		return fmt.Errorf("inspect index directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("index path is not a directory: %s", indexDir)
	}
	matches, err := filepath.Glob(filepath.Join(indexDir, "*.zoekt"))
	if err != nil {
		return fmt.Errorf("inspect index shards: %w", err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("no Zoekt shards found at %s; run %s index --name NAME --source PATH first", indexDir, appName)
	}
	return nil
}
