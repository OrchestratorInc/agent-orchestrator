package usage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
)

type jcodeReadState struct {
	snapshotInfo  os.FileInfo
	journalInfo   os.FileInfo
	ledgerInfo    os.FileInfo
	snapshot      jcodeNativeSession
	native        jcodeNativeSession
	journalOffset int64
	exported      map[string]bool
	synced        bool
}

func sameJCodeFile(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func jcodeFileInfo(ctx context.Context, path string) (os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return info, err
}

func cloneJCodeSession(native jcodeNativeSession) jcodeNativeSession {
	native.Messages = slices.Clone(native.Messages)
	return native
}

var errJCodeSnapshotShape = errors.New("incomplete jcode snapshot")

// decodeJCodeSession streams the envelope and each message separately. Unknown
// fields, including tool content, are skipped rather than retaining the whole
// transcript; only the usage metadata is cached. There is no file-size cutoff.
func decodeJCodeSession(ctx context.Context, decoder *json.Decoder) (jcodeNativeSession, error) {
	var native jcodeNativeSession
	start, err := decoder.Token()
	if err != nil {
		return native, err
	}
	if start != json.Delim('{') {
		return native, errJCodeSnapshotShape
	}
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return native, err
		}
		key, err := decoder.Token()
		if err != nil {
			return native, err
		}
		switch key {
		case "id":
			err = decoder.Decode(&native.ID)
		case "model":
			err = decoder.Decode(&native.Model)
		case "provider_key":
			err = decoder.Decode(&native.ProviderKey)
		case "messages":
			var token json.Token
			token, err = decoder.Token()
			if err == nil && token != json.Delim('[') {
				err = errJCodeSnapshotShape
			}
			if err != nil {
				return native, err
			}
			for decoder.More() {
				if err := ctx.Err(); err != nil {
					return native, err
				}
				var message jcodeNativeMessage
				if err := decoder.Decode(&message); err != nil {
					return native, err
				}
				if message.Role == "assistant" && jsonValuePresent(message.TokenUsage) {
					native.Messages = append(native.Messages, message)
				}
			}
			_, err = decoder.Token()
		default:
			err = skipJCodeValue(ctx, decoder)
		}
		if err != nil {
			return native, err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return native, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return native, errJCodeSnapshotShape
	}
	return native, nil
}

func skipJCodeValue(ctx context.Context, decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	depth := 0
	if token == json.Delim('{') || token == json.Delim('[') {
		depth = 1
	}
	for depth > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err = decoder.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
	}
	return nil
}
