package project

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestRepoNameFromURL(t *testing.T) {
	tests := map[string]string{
		"git@github.com:acme/repo.git":           "repo",
		"git@github.com:acme/repo":               "repo",
		"git@github.com:repo.git":                "repo",
		"https://github.com/acme/repo.git":       "repo",
		"https://github.com/acme/repo":           "repo",
		"https://github.com/acme/repo/":          "repo",
		"ssh://git@host:2222/acme/repo.git":      "repo",
		"https://user:pw@host:8443/a/b/Repo.git": "Repo",
		"/srv/git/local.git":                     "local",
		"":                                       "",
		"https://host/":                          "",
	}
	for in, want := range tests {
		if got := repoNameFromURL(in); got != want {
			t.Errorf("repoNameFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectConfigNameCharacters(t *testing.T) {
	valid := []string{"Mi Proyecto", "repo.v2", "a_b-c", "ñandú"}
	invalid := []string{"a/b", `a\b`, "a\nb", "a:b", "a$b", ".", ".."}
	for _, name := range valid {
		d := newRepo(t, "dir", "")
		body, _ := json.Marshal(map[string]string{"project_name": name})
		writeConfig(t, d, string(body))
		got, err := Detect(context.Background(), d)
		if err != nil || got.DetectedBy != ConfigOverride {
			t.Errorf("%q: got %+v, %v", name, got, err)
		}
	}
	for _, name := range invalid {
		d := newRepo(t, "dir", "")
		body, _ := json.Marshal(map[string]string{"project_name": name})
		writeConfig(t, d, string(body))
		if _, err := Detect(context.Background(), d); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%q: got %v, want ErrInvalidConfig", name, err)
		}
	}
}
