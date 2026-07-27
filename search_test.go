package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchReturnsStructuredMatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var request backendSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Q != `repo:^demo$ ДинамическийСписок` {
			t.Fatalf("unexpected query: %q", request.Q)
		}
		if request.Opts.MaxDocDisplayCount != 20 || request.Opts.NumContextLines != 2 {
			t.Fatalf("defaults were not applied: %+v", request.Opts)
		}

		var reply backendSearchReply
		reply.Result.FileCount = 1
		reply.Result.MatchCount = 1
		reply.Result.Files = []backendFileMatch{{
			FileName:   "ОбщиеМодули/Тест/Модуль.bsl",
			Repository: "demo",
			Language:   "BSL",
			LineMatches: []struct {
				Line       []byte
				LineNumber int
				Before     []byte
				After      []byte
			}{{
				Line:       []byte(`Тип = Новый ОписаниеТипов("ДинамическийСписок");`),
				LineNumber: 7,
				Before:     []byte("Процедура Тест()\n"),
				After:      []byte("КонецПроцедуры\n"),
			}},
		}}
		if err := json.NewEncoder(w).Encode(reply); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	service := NewSearchService("test-index", &BackendClient{
		baseURL: server.URL,
		http:    server.Client(),
	})
	result, err := service.Search(context.Background(), SearchRequest{
		Query: `repo:^demo$ ДинамическийСписок`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FileCount != 1 || result.MatchCount != 1 {
		t.Fatalf("unexpected counts: %+v", result)
	}
	if result.Guidance == "" {
		t.Fatal("search response is missing inference guidance")
	}
	if result.Scope != "explicit-query-filter" || result.EffectiveQuery != result.Query {
		t.Fatalf("explicit repository scope was not preserved: %+v", result)
	}
	if result.Files[0].Repository != "demo" || result.Files[0].Matches[0].LineNumber != 7 {
		t.Fatalf("unexpected structured match: %+v", result.Files[0])
	}
}

func TestScopeQueryUsesParticipantDefaultAndExplicitOverrides(t *testing.T) {
	manifest := &indexManifest{
		SchemaVersion: 1,
		DefaultCorpus: "ERP-2.5.27.58",
		Corpora: []manifestCorpus{
			{Name: "ERP-2.5.27.58"},
			{Name: "ERPУХ"},
		},
	}
	tests := []struct {
		name      string
		input     SearchRequest
		query     string
		scope     string
		corpus    string
		wantError bool
	}{
		{
			name:   "default",
			input:  SearchRequest{Query: "ДинамическийСписок"},
			query:  `repo:"^ERP-2\.5\.27\.58$" ДинамическийСписок`,
			scope:  "default",
			corpus: "ERP-2.5.27.58",
		},
		{
			name:   "explicit corpus",
			input:  SearchRequest{Query: "ДинамическийСписок", Corpus: "erpух"},
			query:  `repo:"^ERPУХ$" ДинамическийСписок`,
			scope:  "explicit-corpus",
			corpus: "ERPУХ",
		},
		{
			name:  "all corpora",
			input: SearchRequest{Query: "ДинамическийСписок", AllCorpora: true},
			query: "ДинамическийСписок",
			scope: "all-corpora",
		},
		{
			name:  "legacy repo filter",
			input: SearchRequest{Query: `repo:^ERPУХ$ ДинамическийСписок`},
			query: `repo:^ERPУХ$ ДинамическийСписок`,
			scope: "explicit-query-filter",
		},
		{
			name: "conflicting selectors",
			input: SearchRequest{
				Query:  `repo:^ERPУХ$ ДинамическийСписок`,
				Corpus: "ERPУХ",
			},
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query, scope, corpus, err := scopeQuery(manifest, test.input.Query, test.input)
			if test.wantError {
				if err == nil {
					t.Fatal("expected scope validation error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if query != test.query || scope != test.scope || corpus != test.corpus {
				t.Fatalf(
					"unexpected scope: query=%q scope=%q corpus=%q",
					query,
					scope,
					corpus,
				)
			}
		})
	}
}

func TestScopeQueryRequiresDefaultForMultipleCorpora(t *testing.T) {
	manifest := &indexManifest{
		SchemaVersion: 1,
		Corpora: []manifestCorpus{
			{Name: "ERP"},
			{Name: "ERPУХ"},
		},
	}
	if _, _, _, err := scopeQuery(
		manifest,
		"ДинамическийСписок",
		SearchRequest{Query: "ДинамическийСписок"},
	); err == nil {
		t.Fatal("expected missing default error")
	}
}

func TestGuidanceExplainsConfigurationAndTemplatePriority(t *testing.T) {
	defaultGuidance := guidanceForScope("default")
	for _, expected := range []string{
		"reusable implementation templates",
		"configuration family",
		"version differs",
	} {
		if !strings.Contains(defaultGuidance, expected) {
			t.Fatalf("default guidance is missing %q: %s", expected, defaultGuidance)
		}
	}
	explicitGuidance := guidanceForScope("explicit-corpus")
	for _, expected := range []string{"configuration family", "exact version match"} {
		if !strings.Contains(explicitGuidance, expected) {
			t.Fatalf("explicit guidance is missing %q: %s", expected, explicitGuidance)
		}
	}
}

func TestSetDefaultCorpusPersistsCanonicalName(t *testing.T) {
	indexDir := t.TempDir()
	manifest := &indexManifest{
		SchemaVersion: 1,
		Corpora: []manifestCorpus{
			{Name: "ERP-2.5.27.58"},
			{Name: "ERPУХ"},
		},
	}
	if err := saveManifest(indexDir, manifest); err != nil {
		t.Fatal(err)
	}
	canonical, err := setDefaultCorpus(indexDir, "erp-2.5.27.58")
	if err != nil {
		t.Fatal(err)
	}
	if canonical != "ERP-2.5.27.58" {
		t.Fatalf("unexpected canonical corpus: %q", canonical)
	}
	loaded, err := loadManifest(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DefaultCorpus != canonical {
		t.Fatalf(
			"default was not persisted in %s: %q",
			filepath.Join(indexDir, manifestFileName),
			loaded.DefaultCorpus,
		)
	}
}

func TestSearchRejectsUnboundedArguments(t *testing.T) {
	service := NewSearchService("test-index", nil)
	if _, err := service.Search(context.Background(), SearchRequest{
		Query:      "test",
		MaxMatches: 1001,
	}); err == nil {
		t.Fatal("expected maxMatches validation error")
	}
}

func TestParseExtensions(t *testing.T) {
	got, err := parseExtensions(" .XML, bsl,BSL ")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "bsl" || got[1] != "xml" {
		t.Fatalf("unexpected extensions: %#v", got)
	}
}

func TestValidateCorpusName(t *testing.T) {
	for _, valid := range []string{"ERPУХ", "zup-demo", "Бухгалтерия 3.0"} {
		if err := validateCorpusName(valid); err != nil {
			t.Fatalf("valid name %q was rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", " trailing ", `path\name`, "name."} {
		if err := validateCorpusName(invalid); err == nil {
			t.Fatalf("invalid name %q was accepted", invalid)
		}
	}
}
