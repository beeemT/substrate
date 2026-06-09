package domain

import "testing"

func TestQuestionSourceDirection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		source  QuestionSource
		human   bool
		foreman bool
	}{
		{source: QuestionSourceAskForeman, foreman: true},
		{source: QuestionSourceAskUser, human: true},
		{source: QuestionSourceClaudeAsk, human: true},
		{source: QuestionSourceOMPAsk, human: true},
		{source: QuestionSourceOpenCodeQuestion, human: true},
		{source: QuestionSourceFutureHarnessQuestion, human: true},
	}

	for _, tc := range cases {
		t.Run(string(tc.source), func(t *testing.T) {
			t.Parallel()
			if got := tc.source.IsHumanDirected(); got != tc.human {
				t.Fatalf("IsHumanDirected() = %v, want %v", got, tc.human)
			}
			if got := tc.source.IsForemanDirected(); got != tc.foreman {
				t.Fatalf("IsForemanDirected() = %v, want %v", got, tc.foreman)
			}
		})
	}
}
