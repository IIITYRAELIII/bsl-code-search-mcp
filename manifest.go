package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const manifestFileName = "bsl-code-search.json"

type indexManifest struct {
	SchemaVersion int              `json:"schemaVersion"`
	DefaultCorpus string           `json:"defaultCorpus,omitempty"`
	Corpora       []manifestCorpus `json:"corpora"`
}

type manifestCorpus struct {
	Name        string    `json:"name"`
	Source      string    `json:"source"`
	Extensions  []string  `json:"extensions"`
	Files       int       `json:"files"`
	SourceBytes int64     `json:"sourceBytes"`
	IndexedAt   time.Time `json:"indexedAt"`
}

func loadManifest(indexDir string) (*indexManifest, error) {
	path := filepath.Join(indexDir, manifestFileName)
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &indexManifest{SchemaVersion: 1}, nil
		}
		return nil, fmt.Errorf("read index manifest: %w", err)
	}
	var manifest indexManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return nil, fmt.Errorf("decode index manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported index manifest schema %d", manifest.SchemaVersion)
	}
	if manifest.DefaultCorpus != "" {
		canonical, ok := findCorpusName(&manifest, manifest.DefaultCorpus)
		if !ok {
			return nil, fmt.Errorf(
				"index manifest default corpus %q is not attached",
				manifest.DefaultCorpus,
			)
		}
		manifest.DefaultCorpus = canonical
	}
	return &manifest, nil
}

func saveManifest(indexDir string, manifest *indexManifest) error {
	if manifest.DefaultCorpus != "" {
		canonical, ok := findCorpusName(manifest, manifest.DefaultCorpus)
		if !ok {
			return fmt.Errorf(
				"default corpus %q is not attached",
				manifest.DefaultCorpus,
			)
		}
		manifest.DefaultCorpus = canonical
	}
	sort.Slice(manifest.Corpora, func(i, j int) bool {
		return manifest.Corpora[i].Name < manifest.Corpora[j].Name
	})
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode index manifest: %w", err)
	}
	content = append(content, '\n')
	path := filepath.Join(indexDir, manifestFileName)
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, content, 0o600); err != nil {
		return fmt.Errorf("write temporary index manifest: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace index manifest: %w", err)
	}
	return nil
}

func updateManifest(indexDir string, corpus manifestCorpus, makeDefault bool) error {
	manifest, err := loadManifest(indexDir)
	if err != nil {
		return err
	}
	replaced := false
	for index := range manifest.Corpora {
		if strings.EqualFold(manifest.Corpora[index].Name, corpus.Name) {
			manifest.Corpora[index] = corpus
			replaced = true
			break
		}
	}
	if !replaced {
		manifest.Corpora = append(manifest.Corpora, corpus)
	}
	if makeDefault {
		manifest.DefaultCorpus = corpus.Name
	}
	return saveManifest(indexDir, manifest)
}

func setDefaultCorpus(indexDir, name string) (string, error) {
	manifest, err := loadManifest(indexDir)
	if err != nil {
		return "", err
	}
	canonical, ok := findCorpusName(manifest, name)
	if !ok {
		return "", fmt.Errorf("corpus %q is not attached", name)
	}
	manifest.DefaultCorpus = canonical
	if err := saveManifest(indexDir, manifest); err != nil {
		return "", err
	}
	return canonical, nil
}

func findCorpusName(manifest *indexManifest, name string) (string, bool) {
	for _, corpus := range manifest.Corpora {
		if strings.EqualFold(corpus.Name, name) {
			return corpus.Name, true
		}
	}
	return "", false
}

func effectiveDefaultCorpus(manifest *indexManifest) string {
	if manifest.DefaultCorpus != "" {
		return manifest.DefaultCorpus
	}
	if len(manifest.Corpora) == 1 {
		return manifest.Corpora[0].Name
	}
	return ""
}

func scanSource(source string, extensions []string) (files int, bytes int64, err error) {
	allowed := make(map[string]struct{}, len(extensions))
	for _, extension := range extensions {
		allowed["."+strings.ToLower(extension)] = struct{}{}
	}
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != source && isIgnoredDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if _, ok := allowed[strings.ToLower(filepath.Ext(entry.Name()))]; !ok {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err
}

func isIgnoredDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".hg", ".svn":
		return true
	default:
		return false
	}
}

func indexDiskStats(indexDir string) (shards int, bytes int64, err error) {
	paths, err := filepath.Glob(filepath.Join(indexDir, "*.zoekt"))
	if err != nil {
		return 0, 0, err
	}
	for _, path := range paths {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return 0, 0, statErr
		}
		shards++
		bytes += info.Size()
	}
	return shards, bytes, nil
}
