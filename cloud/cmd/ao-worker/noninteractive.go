package main

import "os"

// nonInteractiveEnvironment keeps git and its credential helpers from asking
// for input. Every git process the worker or its agents start inherits it.
var nonInteractiveEnvironment = map[string]string{
	"GIT_TERMINAL_PROMPT": "0",
	"GCM_INTERACTIVE":     "never",
}

// disableInteractivePrompts sets the non-interactive defaults without
// overriding a value an operator configured explicitly.
func disableInteractivePrompts() {
	for key, value := range nonInteractiveEnvironment {
		if _, set := os.LookupEnv(key); !set {
			_ = os.Setenv(key, value)
		}
	}
}
