// Command chattraffic validates and compares chat traffic benchmark runs
// and prepares baseline trees. It treats every artifact as data.
//
//	chattraffic compare --baseline B.json --candidate C.json --limits limits.json \
//	    [--expect-baseline-ref SHA] [--expect-candidate-ref SHA] [--allow-client-change] \
//	    [--out-json report.json] [--out-md report.md]
//	chattraffic validate --run R.json
//	chattraffic overlay --candidate-root DIR --baseline-root DIR --manifest OUT.json
//	chattraffic verify-export --export-root DIR --ls-tree FILE --manifest OVERLAY.json
//
// Exit codes: 0 pass, 1 limit or semantic failure, 2 invalid input,
// incomparable runs, or usage error.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/chattraffic"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		note(stderr, "usage: chattraffic compare|validate|overlay|verify-export [flags]")
		return chattraffic.ExitIncomparable
	}
	switch args[0] {
	case "compare":
		return compare(args[1:], stdout, stderr)
	case "validate":
		return validate(args[1:], stdout, stderr)
	case "overlay":
		return overlay(args[1:], stdout, stderr)
	case "verify-export":
		return verifyExport(args[1:], stdout, stderr)
	}
	note(stderr, "unknown command %q", args[0])
	return chattraffic.ExitIncomparable
}

func compare(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baselinePath := fs.String("baseline", "", "baseline run JSON")
	candidatePath := fs.String("candidate", "", "candidate run JSON")
	limitsPath := fs.String("limits", "", "reviewed limits JSON")
	expectBaseline := fs.String("expect-baseline-ref", "", "commit the baseline must have measured")
	expectCandidate := fs.String("expect-candidate-ref", "", "commit the candidate must have measured")
	allowClientChange := fs.Bool("allow-client-change", false, "compare even when renderer source differs from the simulator's")
	outJSON := fs.String("out-json", "", "write the report JSON here")
	outMD := fs.String("out-md", "", "write the Markdown report here")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *baselinePath == "" || *candidatePath == "" || *limitsPath == "" {
		note(stderr, "compare needs --baseline, --candidate and --limits")
		return chattraffic.ExitIncomparable
	}
	limitsBytes, err := chattraffic.ReadArtifact(*limitsPath)
	if err != nil {
		return rejected(stderr, "limits", err)
	}
	limits, err := chattraffic.DecodeLimits(limitsBytes)
	if err != nil {
		return rejected(stderr, "limits", err)
	}
	baseline, err := loadRun(*baselinePath)
	if err != nil {
		return rejected(stderr, "baseline run", err)
	}
	candidate, err := loadRun(*candidatePath)
	if err != nil {
		return rejected(stderr, "candidate run", err)
	}
	report := chattraffic.Compare(baseline, candidate, limits, chattraffic.Options{
		LimitsSha256:       chattraffic.SHA256Hex(limitsBytes),
		ExpectBaselineRef:  *expectBaseline,
		ExpectCandidateRef: *expectCandidate,
		AllowClientChange:  *allowClientChange,
	})
	markdown := chattraffic.RenderMarkdown(report)
	if *outJSON != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil || os.WriteFile(*outJSON, append(data, '\n'), 0o644) != nil { //nolint:gosec // report output.
			note(stderr, "could not write the JSON report")
			return chattraffic.ExitIncomparable
		}
	}
	if *outMD != "" {
		if err := os.WriteFile(*outMD, []byte(markdown), 0o644); err != nil { //nolint:gosec // report output.
			note(stderr, "could not write the Markdown report")
			return chattraffic.ExitIncomparable
		}
	}
	return emit(stdout, stderr, report.ExitCode, markdown)
}

func loadRun(path string) (chattraffic.Run, error) {
	data, err := chattraffic.ReadArtifact(path)
	if err != nil {
		return chattraffic.Run{}, err
	}
	return chattraffic.DecodeRun(data)
}

func rejected(stderr io.Writer, what string, err error) int {
	note(stderr, "rejected %s: %v", what, err)
	return chattraffic.ExitIncomparable
}

func validate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runPath := fs.String("run", "", "run JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *runPath == "" {
		note(stderr, "validate needs --run")
		return chattraffic.ExitIncomparable
	}
	if _, err := loadRun(*runPath); err != nil {
		return rejected(stderr, "run", err)
	}
	return emit(stdout, stderr, chattraffic.ExitPass, "valid\n")
}

func overlay(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("overlay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	candidateRoot := fs.String("candidate-root", "", "repository root holding the harness")
	baselineRoot := fs.String("baseline-root", "", "exported baseline tree")
	manifest := fs.String("manifest", "", "write the overlay manifest here")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *candidateRoot == "" || *baselineRoot == "" || *manifest == "" {
		note(stderr, "overlay needs --candidate-root, --baseline-root and --manifest")
		return chattraffic.ExitIncomparable
	}
	entries, err := chattraffic.Overlay(*candidateRoot, *baselineRoot)
	if err != nil {
		note(stderr, "overlay refused: %v", err)
		return chattraffic.ExitIncomparable
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil || os.WriteFile(*manifest, append(data, '\n'), 0o644) != nil { //nolint:gosec // manifest output.
		note(stderr, "could not write the overlay manifest")
		return chattraffic.ExitIncomparable
	}
	return emit(stdout, stderr, chattraffic.ExitPass, fmt.Sprintf("overlaid %d harness files\n", len(entries)))
}

func verifyExport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify-export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	exportRoot := fs.String("export-root", "", "exported baseline tree")
	lsTreePath := fs.String("ls-tree", "", "output of git ls-tree -r -z --full-tree <commit> -- <paths>")
	manifest := fs.String("manifest", "", "overlay manifest")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *exportRoot == "" || *lsTreePath == "" || *manifest == "" {
		note(stderr, "verify-export needs --export-root, --ls-tree and --manifest")
		return chattraffic.ExitIncomparable
	}
	lsTree, err := os.ReadFile(*lsTreePath)
	if err != nil {
		return rejected(stderr, "ls-tree output", err)
	}
	var entries []chattraffic.OverlayEntry
	manifestBytes, err := os.ReadFile(*manifest)
	if err == nil {
		err = json.Unmarshal(manifestBytes, &entries)
	}
	if err != nil {
		return rejected(stderr, "overlay manifest", errors.New("unreadable"))
	}
	if err := chattraffic.VerifyExport(*exportRoot, lsTree, entries); err != nil {
		note(stderr, "baseline tree verification failed: %v", err)
		return chattraffic.ExitIncomparable
	}
	return emit(stdout, stderr, chattraffic.ExitPass, "baseline tree matches the commit plus the overlay\n")
}

// note writes a diagnostic to stderr. A diagnostic that cannot be written has
// nowhere else to go, so its error is dropped.
func note(stderr io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(stderr, format+"\n", args...)
}

// emit writes a command's result to stdout and returns code. A result that
// cannot be delivered fails the command instead of passing silently.
func emit(stdout, stderr io.Writer, code int, text string) int {
	if _, err := io.WriteString(stdout, text); err != nil {
		note(stderr, "could not write the result: %v", err)
		return chattraffic.ExitIncomparable
	}
	return code
}
