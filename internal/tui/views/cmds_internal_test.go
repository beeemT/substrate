package views

import "testing"

func TestParseArtifactFetchArgsGitLabUsesProjectPathFromMRURL(t *testing.T) {
	t.Parallel()

	identifier, number, ok := parseArtifactFetchArgs(ArtifactItem{
		Provider: "gitlab",
		RepoName: "postback-service",
		Ref:      "!421",
		URL:      "https://gitlab.example.com/backend/postback-service/-/merge_requests/421?diff_id=123#note_456",
	})
	if !ok {
		t.Fatal("parseArtifactFetchArgs returned ok=false")
	}
	if identifier != "backend/postback-service" {
		t.Fatalf("identifier = %q, want %q", identifier, "backend/postback-service")
	}
	if number != 421 {
		t.Fatalf("number = %d, want 421", number)
	}
}

func TestParseArtifactFetchArgsGitHubUsesRepoFromPRURL(t *testing.T) {
	t.Parallel()

	identifier, number, ok := parseArtifactFetchArgs(ArtifactItem{
		Provider: "github",
		RepoName: "rocket",
		Ref:      "#42",
		URL:      "https://github.example.com/acme/rocket/pull/42/files#discussion_r123",
	})
	if !ok {
		t.Fatal("parseArtifactFetchArgs returned ok=false")
	}
	if identifier != "acme/rocket" {
		t.Fatalf("identifier = %q, want %q", identifier, "acme/rocket")
	}
	if number != 42 {
		t.Fatalf("number = %d, want 42", number)
	}
}
