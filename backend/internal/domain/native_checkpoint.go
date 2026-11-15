package domain

import (
	"encoding/json"
	"strings"
)

type NativeCheckpointEvidence struct {
	NativeID string                        `json:"nativeId"`
	Events   []NativeCheckpointObservation `json:"events"`
	Invalid  bool                          `json:"invalid,omitempty"`
}

type NativeCheckpointObservation struct {
	Generation   string `json:"generation"`
	PromptID     string `json:"promptId"`
	SubmissionID string `json:"submissionId,omitempty"`
	Submission   bool   `json:"submission,omitempty"`
	Text         string `json:"text,omitempty"`
	Coordination bool   `json:"coordination,omitempty"`
}

func AppendNativeCheckpoint(encoded, nativeID string, observation NativeCheckpointObservation) string {
	evidence := NativeCheckpointEvidence{NativeID: nativeID}
	if encoded != "" && json.Unmarshal([]byte(encoded), &evidence) != nil {
		evidence.Invalid = true
	}
	if evidence.NativeID != nativeID || observation.Generation == "" ||
		(observation.Submission && observation.SubmissionID == "") ||
		(!observation.Submission && observation.PromptID == "") {
		evidence.Invalid = true
	}
	if !evidence.Invalid {
		duplicate := false
		for _, old := range evidence.Events {
			if old == observation {
				duplicate = true
				break
			}
		}
		if !duplicate {
			if len(evidence.Events) >= 512 {
				evidence.Invalid = true
			} else {
				evidence.Events = append(evidence.Events, observation)
			}
		}
	}
	data, _ := json.Marshal(evidence)
	return string(data)
}

func NativeSubmissionContext(id string) string {
	return "AO transcript correlation ID: " + id + "."
}

func NativeCheckpointTextMatches(checkpoint, replayed string) bool {
	checkpoint = strings.TrimSpace(checkpoint)
	replayed = strings.TrimSpace(SanitizeControlChars(replayed))
	if checkpoint == replayed {
		return true
	}
	parts := strings.Split(checkpoint, "\n[... truncated by AO ...]\n")
	return len(parts) == 2 && strings.HasPrefix(replayed, parts[0]) && strings.HasSuffix(replayed, parts[1])
}
