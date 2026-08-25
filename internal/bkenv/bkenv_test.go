package bkenv

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestParse covers the mapping from raw environment variables to the typed
// view templates read as .Env.Buildkite.
//
// Parse takes a map rather than reading the process environment, so these tests
// are hermetic and can run in parallel.
func TestParse(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		vars   map[string]string
		assert func(t *testing.T, env Env)
	}{
		"maps string fields": {
			vars: map[string]string{
				"BUILDKITE_BRANCH":            "main",
				"BUILDKITE_COMMIT":            "abc123",
				"BUILDKITE_PIPELINE_SLUG":     "my-app",
				"BUILDKITE_MESSAGE":           "fix: thing",
				"BUILDKITE_TAG":               "v1.2.0",
				"BUILDKITE_ORGANIZATION_SLUG": "acme",
				"BUILDKITE_SOURCE":            "webhook",
				"BUILDKITE_BUILD_URL":         "https://buildkite.com/acme/my-app/builds/42",
			},
			assert: func(t *testing.T, env Env) {
				assert.Equal(t, "main", env.Buildkite.Branch)
				assert.Equal(t, "abc123", env.Buildkite.Commit)
				assert.Equal(t, "my-app", env.Buildkite.PipelineSlug)
				assert.Equal(t, "fix: thing", env.Buildkite.Message)
				assert.Equal(t, "v1.2.0", env.Buildkite.Tag)
				assert.Equal(t, "acme", env.Buildkite.Organization)
				assert.Equal(t, "webhook", env.Buildkite.Source)
				assert.Equal(t, "https://buildkite.com/acme/my-app/builds/42", env.Buildkite.BuildURL)
			},
		},
		"an empty environment yields a usable zero value": {
			vars: map[string]string{},
			assert: func(t *testing.T, env Env) {
				assert.Empty(t, env.Buildkite.Branch)
				assert.Zero(t, env.Buildkite.BuildNumber)
				assert.False(t, env.Buildkite.PullRequest.IsPR)
				assert.NotNil(t, env.Vars, "Vars must be non-nil so templates can index it")
			},
		},

		// --- integer fields ---
		"parses build number as an int": {
			vars: map[string]string{"BUILDKITE_BUILD_NUMBER": "42"},
			assert: func(t *testing.T, env Env) {
				assert.Equal(t, 42, env.Buildkite.BuildNumber)
			},
		},
		"parses retry count as an int": {
			vars: map[string]string{"BUILDKITE_RETRY_COUNT": "3"},
			assert: func(t *testing.T, env Env) {
				assert.Equal(t, 3, env.Buildkite.RetryCount)
			},
		},
		"an unparseable int is zero, not an error": {
			vars: map[string]string{"BUILDKITE_BUILD_NUMBER": "not-a-number"},
			assert: func(t *testing.T, env Env) {
				assert.Zero(t, env.Buildkite.BuildNumber)
			},
		},

		// --- the BUILDKITE_PULL_REQUEST footgun ---
		"pull request false is not a PR": {
			// Buildkite sets this to the literal string "false", which is
			// non-empty and therefore truthy in a template. This is the whole
			// reason PullRequest is a struct with a real bool.
			vars: map[string]string{"BUILDKITE_PULL_REQUEST": "false"},
			assert: func(t *testing.T, env Env) {
				assert.False(t, env.Buildkite.PullRequest.IsPR)
				assert.Zero(t, env.Buildkite.PullRequest.Number)
			},
		},
		"a pull request number is a PR": {
			vars: map[string]string{
				"BUILDKITE_PULL_REQUEST":             "123",
				"BUILDKITE_PULL_REQUEST_BASE_BRANCH": "main",
				"BUILDKITE_PULL_REQUEST_REPO":        "git://github.com/acme/my-app.git",
			},
			assert: func(t *testing.T, env Env) {
				assert.True(t, env.Buildkite.PullRequest.IsPR)
				assert.Equal(t, 123, env.Buildkite.PullRequest.Number)
				assert.Equal(t, "main", env.Buildkite.PullRequest.BaseBranch)
				assert.Equal(t, "git://github.com/acme/my-app.git", env.Buildkite.PullRequest.Repo)
			},
		},
		"a missing pull request var is not a PR": {
			vars: map[string]string{},
			assert: func(t *testing.T, env Env) {
				assert.False(t, env.Buildkite.PullRequest.IsPR)
			},
		},
		"an empty pull request var is not a PR": {
			vars: map[string]string{"BUILDKITE_PULL_REQUEST": ""},
			assert: func(t *testing.T, env Env) {
				assert.False(t, env.Buildkite.PullRequest.IsPR)
			},
		},
		"an unparseable pull request var is not a PR": {
			vars: map[string]string{"BUILDKITE_PULL_REQUEST": "true"},
			assert: func(t *testing.T, env Env) {
				assert.False(t, env.Buildkite.PullRequest.IsPR,
					"only a numeric value identifies a pull request")
				assert.Zero(t, env.Buildkite.PullRequest.Number)
			},
		},
		"pull request zero is not a PR": {
			// Buildkite numbers pull requests from 1, so a parseable but
			// non-positive value is not a real PR reference.
			vars: map[string]string{"BUILDKITE_PULL_REQUEST": "0"},
			assert: func(t *testing.T, env Env) {
				assert.False(t, env.Buildkite.PullRequest.IsPR,
					"a PR number must be positive")
				assert.Zero(t, env.Buildkite.PullRequest.Number)
			},
		},
		"a negative pull request number is not a PR": {
			vars: map[string]string{"BUILDKITE_PULL_REQUEST": "-5"},
			assert: func(t *testing.T, env Env) {
				assert.False(t, env.Buildkite.PullRequest.IsPR,
					"a PR number must be positive")
				assert.Zero(t, env.Buildkite.PullRequest.Number,
					"a rejected value must not leak into Number")
			},
		},
		"pull request one is the lowest real PR": {
			vars: map[string]string{"BUILDKITE_PULL_REQUEST": "1"},
			assert: func(t *testing.T, env Env) {
				assert.True(t, env.Buildkite.PullRequest.IsPR)
				assert.Equal(t, 1, env.Buildkite.PullRequest.Number)
			},
		},

		// --- Vars is the unfiltered long tail ---
		"non-buildkite vars land in Vars": {
			vars: map[string]string{"MY_CUSTOM": "hello", "BUILDKITE_BRANCH": "main"},
			assert: func(t *testing.T, env Env) {
				assert.Equal(t, "hello", env.Vars["MY_CUSTOM"])
			},
		},
		"Vars also contains buildkite vars unmodified": {
			// Vars is a convenience view over everything, not a partition: a var
			// being typed does not remove it from the raw map.
			vars: map[string]string{"BUILDKITE_BRANCH": "main"},
			assert: func(t *testing.T, env Env) {
				assert.Equal(t, "main", env.Vars["BUILDKITE_BRANCH"])
			},
		},
		"an absent var reads as empty rather than panicking": {
			vars: map[string]string{},
			assert: func(t *testing.T, env Env) {
				assert.Empty(t, env.Vars["NOPE"])
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, Parse(tc.vars))
		})
	}
}

// TestParseDoesNotAliasInput guards against Parse handing back the caller's map
// as Vars, which would let a template's view change underneath it.
func TestParseDoesNotAliasInput(t *testing.T) {
	t.Parallel()

	vars := map[string]string{"BUILDKITE_BRANCH": "main"}

	env := Parse(vars)
	vars["BUILDKITE_BRANCH"] = "changed"
	vars["ADDED_LATER"] = "surprise"

	assert.Equal(t, "main", env.Vars["BUILDKITE_BRANCH"],
		"Vars must be a copy of the input")
	assert.Empty(t, env.Vars["ADDED_LATER"])
}

// TestParseNilMap keeps the zero case safe: cmd builds the map from
// os.Environ(), but tests and future callers may pass nil.
func TestParseNilMap(t *testing.T) {
	t.Parallel()

	env := Parse(nil)

	assert.NotNil(t, env.Vars)
	assert.Empty(t, env.Buildkite.Branch)
	assert.False(t, env.Buildkite.PullRequest.IsPR)
}
