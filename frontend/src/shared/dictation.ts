export type DictationEvent =
	| { type: "listening" }
	/** Loudness in 0...1, one value per 10 ms of audio, oldest first. */
	| { type: "levels"; values: number[] }
	| { type: "text"; text: string }
	/** Listening stopped; the final wording follows as "end". */
	| { type: "finishing" }
	| { type: "end"; text: string }
	| { type: "error"; message: string }
	/** Another surface started dictating; this one's run ends with what it has. */
	| { type: "superseded" }
	| { type: "exit" };

/**
 * An event as main delivers it: tagged with the run number the surface passed
 * to `dictation:start`, so a late event from an earlier run (a "superseded" in
 * flight, say) cannot end the run started after it.
 */
export type DictationMessage = DictationEvent & { run: number };
