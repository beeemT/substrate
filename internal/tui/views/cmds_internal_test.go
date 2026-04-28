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
