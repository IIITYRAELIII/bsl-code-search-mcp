package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if result.Files[0].Repository != "demo" || result.Files[0].Matches[0].LineNumber != 7 {
		t.Fatalf("unexpected structured match: %+v", result.Files[0])
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
