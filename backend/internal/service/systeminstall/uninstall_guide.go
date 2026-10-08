package systeminstall

// UninstallGuide tells a user how to remove a vendor install that AO does not
// remove itself. AO only shows it; it never runs these paths. Unix paths use
// ~ and Windows paths %USERPROFILE% for the home directory.
type UninstallGuide struct {
	DocsURL string `json:"docsUrl,omitempty" description:"Vendor page with uninstall instructions."`
	// ProgramPaths remove the program itself.
	ProgramPaths []string `json:"programPaths" description:"Program files and directories to remove."`
	// UserDataPaths hold settings, sign-in and history; removing them is optional.
	UserDataPaths []string `json:"userDataPaths,omitempty" description:"Optional settings, sign-in and history paths."`
	// Documented is true when the vendor documents these paths, false when
	// they come from its install script.
	Documented bool `json:"documented" description:"Whether the vendor documents these paths."`
}

type uninstallGuideSpec struct {
	docsURL               string
	unix, windows         []string
	unixData, windowsData []string
	documented            bool
}

// uninstallGuides lists, per harness, what its vendor installer writes. Keep
// each entry to paths confirmed in the vendor's documentation or install
// script; an unconfirmed harness has no entry and shows only its docs link.
var uninstallGuides = map[Target]uninstallGuideSpec{
	TargetClaudeCode: {
		docsURL:     "https://code.claude.com/docs/en/installation#uninstall-claude-code",
		unix:        []string{"~/.local/bin/claude", "~/.local/share/claude"},
		windows:     []string{`%USERPROFILE%\.local\bin\claude.exe`, `%USERPROFILE%\.local\share\claude`},
		unixData:    []string{"~/.claude", "~/.claude.json"},
		windowsData: []string{`%USERPROFILE%\.claude`, `%USERPROFILE%\.claude.json`},
		documented:  true,
	},
}

func uninstallGuideFor(target Target, goos string) *UninstallGuide {
	spec, ok := uninstallGuides[target]
	if !ok {
		return nil
	}
	program, data := spec.unix, spec.unixData
	if goos == "windows" {
		program, data = spec.windows, spec.windowsData
	}
	if len(program) == 0 {
		return nil
	}
	return &UninstallGuide{DocsURL: spec.docsURL, ProgramPaths: program, UserDataPaths: data, Documented: spec.documented}
}
