package main

import (
	"context"
	"flag"
	"os"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/akijowski/pipefitter/internal/cmd"
)

// update regenerates the golden output embedded in the scripts, for when a
// change to the serializer is intended rather than a regression.
var update = flag.Bool("update", false, "update testscript golden files")

// TestMain registers pipefitter as a command the scripts can exec.
//
// It runs the real entry point, so the scripts cover what the in-process tests
// deliberately cannot: OSHost building a Host from the actual working directory
// and environment, process exit codes, and stdout and stderr as real streams.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"pipefitter": func() {
			os.Exit(runMain(context.Background(), cmd.OSHost(), os.Args[1:]))
		},
	})
}

// TestScripts runs every txtar file in testdata/script.
//
// Run a single one with:
//
//	go test . -run 'TestScripts/generate'
//
// Regenerate the golden files after an intentional output change with:
//
//	go test . -run TestScripts -update
func TestScripts(t *testing.T) {
	t.Parallel()

	testscript.Run(t, testscript.Params{
		Dir:                 "testdata/script",
		RequireExplicitExec: true,
		UpdateScripts:       *update,
	})
}
