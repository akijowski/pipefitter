package validate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akijowski/pipefitter/internal/pipeline"
)

// TestDependsOn covers the one rule pipefitter can check that Buildkite cannot
// usefully report: a dependency naming a step that does not exist.
//
// depends_on has three shapes — a string, a list of strings, and a list of
// objects carrying a "step" field — so each is exercised.
func TestDependsOn(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		doc       pipeline.Document
		wantCount int
		wantIn    []string
	}{
		// --- resolved: no findings ---
		"a resolved string dependency passes": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "test"},
				map[string]any{"key": "deploy", "depends_on": "test"},
			}},
		},
		"a resolved list dependency passes": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "a"},
				map[string]any{"key": "b"},
				map[string]any{"key": "c", "depends_on": []any{"a", "b"}},
			}},
		},
		"a resolved object dependency passes": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "test"},
				map[string]any{"key": "deploy", "depends_on": []any{
					map[string]any{"step": "test", "allow_failure": true},
				}},
			}},
		},
		"a forward reference passes": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "deploy", "depends_on": "test"},
				map[string]any{"key": "test"},
			}},
		},

		// --- dangling: one finding each ---
		"a dangling string dependency is reported": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "deploy", "depends_on": "nope"},
			}},
			wantCount: 1,
			wantIn:    []string{"nope", "deploy"},
		},
		"a dangling entry in a list is reported": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "test"},
				map[string]any{"key": "deploy", "depends_on": []any{"test", "missing"}},
			}},
			wantCount: 1,
			wantIn:    []string{"missing"},
		},
		"a dangling object dependency is reported": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "deploy", "depends_on": []any{
					map[string]any{"step": "gone", "allow_failure": true},
				}},
			}},
			wantCount: 1,
			wantIn:    []string{"gone"},
		},
		"every dangling reference is reported, not just the first": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "deploy", "depends_on": []any{"one", "two"}},
			}},
			wantCount: 2,
		},

		// --- group steps ---
		"a key inside a group is collected": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"group": "Tests", "key": "tests", "steps": []any{
					map[string]any{"key": "unit", "command": "go test ./..."},
				}},
				map[string]any{"key": "deploy", "depends_on": "unit"},
			}},
		},
		"a group's own key is collected": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"group": "Tests", "key": "tests", "steps": []any{
					map[string]any{"command": "go test ./..."},
				}},
				map[string]any{"key": "deploy", "depends_on": "tests"},
			}},
		},
		"a dependency inside a group is checked": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"group": "Tests", "steps": []any{
					map[string]any{"key": "unit", "depends_on": "nope"},
				}},
			}},
			wantCount: 1,
			wantIn:    []string{"nope"},
		},

		// --- nothing to check ---
		"an empty document is fine": {
			doc: pipeline.Document{},
		},
		"steps with no dependencies are fine": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "test"},
			}},
		},
		"a bare string step is skipped": {
			doc: pipeline.Document{"steps": []any{"wait"}},
		},
		"a step with no key can still depend on one": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "test"},
				map[string]any{"command": "deploy.sh", "depends_on": "test"},
			}},
		},
		"a null dependency is skipped": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "test", "depends_on": nil},
			}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := DependsOn{}.Check(tc.doc)

			assert.Len(t, got, tc.wantCount)

			var joined string
			for _, f := range got {
				joined += f.Message + "\n"
			}

			for _, want := range tc.wantIn {
				assert.Contains(t, joined, want)
			}
		})
	}
}

func TestDependsOnFindingNamesItsRule(t *testing.T) {
	t.Parallel()

	got := DependsOn{}.Check(pipeline.Document{"steps": []any{
		map[string]any{"key": "deploy", "depends_on": "nope"},
	}})

	require.Len(t, got, 1)
	assert.Equal(t, DependsOn{}.Name(), got[0].Rule)
	assert.NotEmpty(t, DependsOn{}.Name())
}
