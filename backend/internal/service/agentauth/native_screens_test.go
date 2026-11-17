package agentauth

import (
	"strings"
	"testing"
)

// nativeScreens are first screens of real native sign-in and setup flows, as
// AO's hidden terminal sees them on each platform. A screen AO cannot drive
// must hand off to the terminal ("terminal_required"), never sit at
// "waiting": the panel would otherwise show a spinner with nothing to do.
var nativeScreens = []struct {
	name     string
	output   string
	kind     string
	selected string
	options  []string
}{
	{
		// Captured in the desktop app on Windows 11: cliclack falls back to
		// ASCII glyphs there.
		name:   "goose configure, Windows console",
		output: "This will update your existing config files\n  if you prefer, you can edit them directly at C:\\Users\\me\\AppData\\Roaming\\Block\\goose\\config\n\nT  goose-configure\n|\n*  What would you like to configure?\n|  > Configure Providers (Change provider or update credentials)\n|    Custom Providers\n|    Add Extension\n|    Toggle Extensions\n|    Remove Extension\n|    goose settings\n—\n",
		kind:   "terminal_required",
	},
	{
		name:   "goose configure, Windows console with CRLF and colour",
		output: "T  \x1b[46mgoose-configure\x1b[0m\r\n\x1b[36m|\x1b[0m\r\n\x1b[36m*\x1b[0m  What would you like to configure?\r\n\x1b[36m|\x1b[0m  \x1b[36m>\x1b[0m Configure Providers \x1b[2m(Change provider or update credentials)\x1b[0m\r\n\x1b[36m|\x1b[0m    Custom Providers\r\n\x1b[36m—\x1b[0m\r\n",
		kind:   "terminal_required",
	},
	{
		// cliclack's Unicode glyphs on macOS and Linux.
		name:   "goose configure, macOS and Linux",
		output: "┌   goose-configure\n│\n◆  What would you like to configure?\n│  ● Configure Providers (Change provider or update credentials)\n│  ○ Custom Providers\n│  ○ Add Extension\n└\n",
		kind:   "terminal_required",
	},
	{
		// @clack/prompts select, as OpenCode-style provider pickers draw it.
		name:     "clack provider picker with reviewed labels",
		output:   "┌  Add credential\n│\n◆  Select provider\n│  ● OpenAI\n│  ○ Anthropic\n│  ○ OpenRouter\n└\n",
		kind:     "menu",
		selected: "OpenAI",
	},
	{
		name:   "clack provider picker with an unreviewed label",
		output: "┌  Add credential\n│\n◆  Select provider\n│  ● Anthropic (recommended)\n│  ○ OpenAI\n└\n",
		kind:   "terminal_required",
	},
	{
		name:   "clack picker, Windows console",
		output: "T  Add credential\r\n|\r\n*  Select provider\r\n|  > OpenRouter\r\n|    OpenAI\r\n—\r\n",
		kind:   "menu", selected: "OpenRouter",
	},
	{
		name:    "line-oriented numbered menu",
		output:  "Choose a provider:\n1. Anthropic\n2. OpenAI\nEnter choice: ",
		kind:    "menu",
		options: []string{"Anthropic", "OpenAI"},
	},
	{
		name:   "API key prompt",
		output: "┌  Add credential\n│\n◇  Select provider\n│  OpenAI\n│\n◆  Enter your API key\n│  _\n└\n",
		kind:   "terminal_required",
	},
	{
		name:   "starting up",
		output: "Loading configuration…\n",
		kind:   "waiting",
	},
}

func TestNativePromptReadsRealSignInScreens(t *testing.T) {
	for _, screen := range nativeScreens {
		t.Run(screen.name, func(t *testing.T) {
			kind, selected, options, _ := nativePrompt(screen.output)
			if kind != screen.kind || selected != screen.selected || strings.Join(options, ",") != strings.Join(screen.options, ",") {
				t.Fatalf("nativePrompt = %q %q %v, want %q %q %v", kind, selected, options, screen.kind, screen.selected, screen.options)
			}
		})
	}
}
