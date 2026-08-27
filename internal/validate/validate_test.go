package validate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akijowski/pipefitter/internal/pipeline"
)

func TestRun(t *testing.T) {
	t.Parallel()

	doc := pipeline.Document{"steps": []any{
		map[string]any{"key": "deploy", "depends_on": "nope"},
	}}

	assert.NotEmpty(t, Run(doc, Rules()), "the default rule set catches a dangling dependency")
	assert.Empty(t, Run(doc, nil), "no rules means no findings")
}

// TestRunAggregatesEveryRule pins that Run does not stop at the first rule to
// report something, so one failure cannot mask another.
func TestRunAggregatesEveryRule(t *testing.T) {
	t.Parallel()

	always := stubRule{name: "always", findings: []Finding{{Rule: "always", Message: "a"}}}
	never := stubRule{name: "never"}

	got := Run(pipeline.Document{}, []Rule{always, never, always})

	assert.Len(t, got, 2)
}

func TestRulesIsNotEmpty(t *testing.T) {
	t.Parallel()

	rules := Rules()

	require.NotEmpty(t, rules)

	for _, r := range rules {
		assert.NotEmpty(t, r.Name(), "every rule needs a name")
	}
}

type stubRule struct {
	name     string
	findings []Finding
}

func (s stubRule) Name() string { return s.name }

func (s stubRule) Check(pipeline.Document) []Finding { return s.findings }
