package cursorstore

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
)

type graphReader struct {
	ctx   context.Context
	tx    *sql.Tx
	count int
	bytes int
	text  int
}

func (g *graphReader) blob(id []byte) (map[uint64][][]byte, error) {
	g.count++
	if len(id) != 32 || g.count > 100000 {
		return nil, errors.New("invalid cursor native graph reference")
	}
	var body []byte
	if err := g.tx.QueryRowContext(g.ctx, "SELECT CASE WHEN length(data)<=67108864 THEN data END FROM blobs WHERE id=?", hex.EncodeToString(id)).Scan(&body); err != nil {
		return nil, err
	}
	if len(body) == 0 || len(body) > maxBlob {
		return nil, errors.New("missing or oversized cursor native graph blob")
	}
	g.bytes += len(body)
	if g.bytes > maxStore {
		return nil, errors.New("cursor native graph exceeds import limit")
	}
	return decode(body)
}

func (g *graphReader) append(out *Archive, role string, parts [][]byte) error {
	for _, part := range parts {
		if strings.TrimSpace(string(part)) == "" {
			continue
		}
		g.text += len(part)
		if g.text > maxText || len(out.Messages) >= 100000 {
			return errors.New("cursor visible conversation exceeds import limit")
		}
		out.Messages = append(out.Messages, Message{Role: role, Text: string(part), At: out.Updated})
	}
	return nil
}

// decode retains protobuf field numbers and skips unknown fixed-width fields.
func decode(body []byte) (map[uint64][][]byte, error) {
	out := make(map[uint64][][]byte)
	for len(body) != 0 {
		tag, n := binary.Uvarint(body)
		if n <= 0 || tag>>3 == 0 {
			return nil, errors.New("invalid cursor native protobuf tag")
		}
		body = body[n:]
		var value []byte
		switch tag & 7 {
		case 0:
			_, n = binary.Uvarint(body)
			if n <= 0 {
				return nil, errors.New("invalid cursor native protobuf integer")
			}
			value, body = body[:n], body[n:]
		case 1:
			if len(body) < 8 {
				return nil, errors.New("truncated cursor native protobuf fixed64")
			}
			body = body[8:]
		case 2:
			size, n := binary.Uvarint(body)
			if n <= 0 || size > maxBlob {
				return nil, errors.New("invalid cursor native protobuf length")
			}
			length := int(size)
			body = body[n:]
			if length > len(body) {
				return nil, errors.New("invalid cursor native protobuf length")
			}
			value, body = body[:length], body[length:]
		case 5:
			if len(body) < 4 {
				return nil, errors.New("truncated cursor native protobuf fixed32")
			}
			body = body[4:]
		default:
			return nil, errors.New("unsupported cursor native protobuf wire type")
		}
		if value != nil {
			out[tag>>3] = append(out[tag>>3], value)
		}
	}
	return out, nil
}
