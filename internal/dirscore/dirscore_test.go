package dirscore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSplitByCase(t *testing.T) {
	cases := map[string][]string{
		"fooBarBAZQux":      {"foo", "Bar", "BAZ", "Qux"},
		"user_login":        {"user", "login"},
		"HTTPServer2Client": {"HTTP", "Server2", "Client"},
		"parseJSON":         {"parse", "JSON"},
	}
	for input, want := range cases {
		got := splitByCase(input)
		if len(got) != len(want) {
			t.Fatalf("splitByCase(%q) = %v, want %v", input, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("splitByCase(%q) = %v, want %v", input, got, want)
			}
		}
	}
}

func TestStem(t *testing.T) {
	cases := map[string]string{
		"handlers":       "handler",
		"authentication": "authenticate",
		"queries":        "query",
		"logging":        "logg",
		"ab":             "ab",
	}
	for input, want := range cases {
		if got := Stem(input); got != want {
			t.Fatalf("Stem(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTokenizeDropsStopwords(t *testing.T) {
	tokens := Tokenize("where is the userLogin handled")
	for _, tok := range tokens {
		if tok == "the" || tok == "where" || tok == "is" {
			t.Fatalf("stopword leaked into tokens: %v", tokens)
		}
	}
	found := false
	for _, tok := range tokens {
		if tok == "login" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected login token, got %v", tokens)
	}
}

func TestScoreDirectoriesPrefersMatchingDir(t *testing.T) {
	root := t.TempDir()
	mk := func(dir, file, content string) {
		full := filepath.Join(root, dir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("auth", "login.go", "package auth\nfunc Login() {}\n")
	mk("auth", "jwt.go", "package auth\nfunc VerifyJWT() {}\n")
	mk("billing", "invoice.go", "package billing\nfunc Invoice() {}\n")
	mk("docs", "readme.md", "# docs\n")

	result := ScoreDirectories("user login authentication jwt", root,
		[]string{"auth", "billing", "docs"}, nil,
		Options{TopK: 2, UseProbe: false, MinReturn: 2})
	if len(result.HotDirs) == 0 {
		t.Fatal("no hot dirs returned")
	}
	if result.HotDirs[0] != "auth" {
		t.Fatalf("expected auth as top hot dir, got %v", result.HotDirs)
	}
	foundSpine := false
	for _, spine := range result.PathSpines {
		if spine == "auth/login.go" || spine == "auth/jwt.go" {
			foundSpine = true
		}
	}
	if !foundSpine {
		t.Fatalf("expected auth files in path spines, got %v", result.PathSpines)
	}
}

func TestAdaptiveTopKBounds(t *testing.T) {
	fused := []dirScore{
		{"a", 0.9}, {"b", 0.8}, {"c", 0.2}, {"d", 0.1}, {"e", 0.05},
	}
	hot := adaptiveTopK(fused, 4, 5)
	if len(hot) < kMin {
		t.Fatalf("adaptiveTopK returned fewer than kMin dirs: %v", hot)
	}
	if hot[0] != "a" || hot[1] != "b" {
		t.Fatalf("adaptiveTopK order wrong: %v", hot)
	}
}
