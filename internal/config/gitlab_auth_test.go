package config

import "testing"

func TestParseGlabAuthStatusHost(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name: "self-hosted instance",
			output: `git.company.com
  ✓ Logged in to git.company.com as alice (/home/alice/.config/glab-cli/config.yml)
  ✓ Git operations for git.company.com configured to use https protocol.
  ✓ REST API Endpoint: https://git.company.com/api/v4/
`,
			want: "git.company.com",
		},
		{
			name: "gitlab.com",
			output: `gitlab.com
  ✓ Logged in to gitlab.com as bob (keyring)
  ✓ REST API Endpoint: https://gitlab.com/api/v4/
`,
			want: "gitlab.com",
		},
		{
			name: "multiple hosts — first wins",
			output: `git.company.com
  ✓ Logged in to git.company.com as alice (/home/alice/.config/glab-cli/config.yml)

gitlab.com
  ✓ Logged in to gitlab.com as bob (keyring)
`,
			want: "git.company.com",
		},
		{
			name:   "empty output",
			output: "",
			want:   "",
		},
		{
			name:   "not authenticated",
			output: "You are not logged into any GitLab instances. Run `glab auth login` to authenticate.",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGlabAuthStatusHost(tt.output)
			if got != tt.want {
				t.Errorf("parseGlabAuthStatusHost() = %q, want %q", got, tt.want)
			}
		})
	}
}
