package workerexec

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ClaudeConversationSettingsAfter reads the model recorded by Claude's own
// transcript after AO's last explicit Chat selection. Missing history is not
// evidence of a change, so callers keep their durable selection in that case.
func ClaudeConversationSettingsAfter(dataDir, conversationID string, selectedAt time.Time) (string, string, error) {
	if conversationID == "" || filepath.Base(conversationID) != conversationID || strings.ContainsAny(conversationID, `/\\`) {
		return "", "", nil
	}
	root := filepath.Join(dataDir, "claude", "projects")
	var latest time.Time
	var model, effort string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != conversationID+".jsonl" {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
		for scanner.Scan() {
			var event struct {
				Type        string    `json:"type"`
				Timestamp   time.Time `json:"timestamp"`
				IsSidechain bool      `json:"isSidechain"`
				Message     struct {
					Model  string `json:"model"`
					Effort string `json:"effort"`
				} `json:"message"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Type != "assistant" || event.IsSidechain || !event.Timestamp.After(selectedAt) || event.Timestamp.Before(latest) {
				continue
			}
			if value := strings.TrimSpace(event.Message.Model); value != "" && !strings.HasPrefix(value, "<") {
				latest, model, effort = event.Timestamp, value, strings.TrimSpace(event.Message.Effort)
			}
		}
		return scanner.Err()
	})
	if os.IsNotExist(err) {
		return "", "", nil
	}
	return model, effort, err
}
