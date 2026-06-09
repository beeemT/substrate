package adapter

import "testing"

func TestQuestionToolPolicyTarget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		policy        QuestionToolPolicy
		defaultTarget QuestionToolTarget
		want          QuestionToolTarget
	}{
		{name: "default uses harness target", policy: QuestionToolPolicyDefault, defaultTarget: QuestionToolTargetForeman, want: QuestionToolTargetForeman},
		{name: "foreman overrides default", policy: QuestionToolPolicyForeman, defaultTarget: QuestionToolTargetHuman, want: QuestionToolTargetForeman},
		{name: "human overrides default", policy: QuestionToolPolicyHuman, defaultTarget: QuestionToolTargetForeman, want: QuestionToolTargetHuman},
		{name: "none overrides default", policy: QuestionToolPolicyNone, defaultTarget: QuestionToolTargetBoth, want: QuestionToolTargetNone},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.policy.Target(tc.defaultTarget); got != tc.want {
				t.Fatalf("Target() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestQuestionToolTargetAllowedQuestions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		target  QuestionToolTarget
		foreman bool
		human   bool
	}{
		{target: QuestionToolTargetNone},
		{target: QuestionToolTargetForeman, foreman: true},
		{target: QuestionToolTargetHuman, human: true},
		{target: QuestionToolTargetBoth, foreman: true, human: true},
	}

	for _, tc := range cases {
		t.Run(string(tc.target), func(t *testing.T) {
			t.Parallel()
			if got := tc.target.AllowsForemanQuestions(); got != tc.foreman {
				t.Fatalf("AllowsForemanQuestions() = %v, want %v", got, tc.foreman)
			}
			if got := tc.target.AllowsHumanQuestions(); got != tc.human {
				t.Fatalf("AllowsHumanQuestions() = %v, want %v", got, tc.human)
			}
		})
	}
}
