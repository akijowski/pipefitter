// Package bkenv turns the raw process environment into the typed view that
// templates read as .Env.
//
// Parse takes a map rather than reading the environment itself, so everything
// here is a pure function of its input and tests never touch process state.
// Building that map is the caller's job — see internal/cmd.
package bkenv

import (
	"maps"
	"strconv"
)

// Env is what a template sees as .Env.
//
// Buildkite is a curated, typed view of the variables worth having types for.
// Vars is the whole environment, unfiltered — it is a fallback for variables we
// have not modeled, not a partition, so a variable appearing in Buildkite is
// still present in Vars under its original name.
//
// Vars is always non-nil, so {{ .Env.Vars.ANYTHING }} yields empty rather than
// failing.
type Env struct {
	Buildkite Buildkite
	Vars      map[string]string
}

// Buildkite is the curated set of BUILDKITE_* variables, typed.
//
// The set is deliberately partial; variables get promoted here as templates need
// them, and the long tail stays reachable through Env.Vars.
//
// Values that fail to parse as integers fall back to zero rather than raising an
// error, because a malformed variable should not fail a pipeline render. Zero is
// safe for each of them for a different reason: RetryCount of 0 is the correct
// value for a first attempt, Buildkite numbers builds from 1 so a BuildNumber of
// 0 cannot occur in a real build, and PullRequest carries its own IsPR flag
// rather than relying on a sentinel number.
type Buildkite struct {
	Branch, Commit, Tag, Message string
	PipelineSlug, Organization   string
	BuildNumber                  int
	BuildURL                     string
	Source                       string
	RetryCount                   int
	PullRequest                  PullRequest
}

// PullRequest describes the pull request a build belongs to, if any.
//
// IsPR exists to handle an edge case: Buildkite sets BUILDKITE_PULL_REQUEST to
// the literal string "false" when a build is not for a pull request, and that
// string is non-empty and therefore truthy in a template. Branch on IsPR, never
// on the raw variable.
//
// IsPR is true only when the variable parses as a positive integer, so "false",
// "true", "0", "-5", empty and absent all read as "not a pull request". The
// remaining fields are only meaningful when IsPR is true.
type PullRequest struct {
	IsPR       bool
	Number     int
	BaseBranch string
	Repo       string
}

// Parse builds an Env from the given environment variables.
//
// The returned Vars is a copy, so later changes to the caller's map are not
// visible to a render already in progress, and a nil input yields an empty
// non-nil map. Recognized BUILDKITE_* variables are additionally parsed into the
// Buildkite struct; unparseable integers fall back to zero, and Parse never
// fails.
func Parse(vars map[string]string) Env {
	m := make(map[string]string, len(vars))
	maps.Copy(m, vars)

	return Env{
		Buildkite: parseBuildkite(m),
		Vars:      m,
	}
}

// parseBuildkite reads the recognized variables by full name. Looking each one
// up directly, rather than iterating the environment and matching a prefix,
// keeps the literal variable names greppable — which is what you want when
// working out why a field came back empty.
func parseBuildkite(vars map[string]string) Buildkite {
	bk := Buildkite{
		Branch:       vars["BUILDKITE_BRANCH"],
		Commit:       vars["BUILDKITE_COMMIT"],
		Tag:          vars["BUILDKITE_TAG"],
		Message:      vars["BUILDKITE_MESSAGE"],
		PipelineSlug: vars["BUILDKITE_PIPELINE_SLUG"],
		Organization: vars["BUILDKITE_ORGANIZATION_SLUG"],
		// intentionally left a string and not *url.Url
		BuildURL: vars["BUILDKITE_BUILD_URL"],
		Source:   vars["BUILDKITE_SOURCE"],
		PullRequest: PullRequest{
			BaseBranch: vars["BUILDKITE_PULL_REQUEST_BASE_BRANCH"],
			Repo:       vars["BUILDKITE_PULL_REQUEST_REPO"],
		},
	}

	if n, err := strconv.Atoi(vars["BUILDKITE_BUILD_NUMBER"]); err == nil {
		bk.BuildNumber = n
	}

	if n, err := strconv.Atoi(vars["BUILDKITE_RETRY_COUNT"]); err == nil {
		bk.RetryCount = n
	}

	if n, err := strconv.Atoi(vars["BUILDKITE_PULL_REQUEST"]); err == nil && n > 0 {
		bk.PullRequest.IsPR = true
		bk.PullRequest.Number = n
	}

	return bk
}
