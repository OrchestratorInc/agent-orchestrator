package domain

type ChatExcerptSnapshot struct {
	ReferenceFormat   string                   `json:"referenceFormat,omitempty" enum:"numbered-v1"`
	ContainsFlowchart bool                     `json:"containsFlowchart,omitempty"`
	BadgeOnly         bool                     `json:"badgeOnly,omitempty"`
	Version           int                      `json:"version"`
	RawText           string                   `json:"rawText"`
	CaptureMode       string                   `json:"captureMode" enum:"exact,block,legacy"`
	SourceTurnID      string                   `json:"sourceTurnId,omitempty"`
	SourceRange       *ChatExcerptRange        `json:"sourceRange,omitempty"`
	RenderedRange     *ChatExcerptRange        `json:"renderedRange,omitempty"`
	DiagramRange      *ChatExcerptRange        `json:"diagramRange,omitempty"`
	Prefix            string                   `json:"prefix,omitempty"`
	Suffix            string                   `json:"suffix,omitempty"`
	Turn              *ChatExcerptTurnSnapshot `json:"turn,omitempty"`
	ContextOmitted    bool                     `json:"contextOmitted,omitempty"`
}

type ChatExcerptRange struct {
	Start      int    `json:"start"`
	End        int    `json:"end"`
	OffsetUnit string `json:"offsetUnit" enum:"utf-16"`
}

type ChatExcerptTurnSnapshot struct {
	ID        string                   `json:"id"`
	Messages  []ChatExcerptTurnMessage `json:"messages"`
	Truncated bool                     `json:"truncated,omitempty"`
}

type ChatExcerptTurnMessage struct {
	Role string `json:"role" enum:"user,assistant"`
	Text string `json:"text"`
}
