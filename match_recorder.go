package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const recordTypeWebSocket = "websocket"

type recordedPayload struct {
	Type    string          `json:"type"`
	Time    int64           `json:"time"`
	Payload json.RawMessage `json:"payload"`
}

type pendingMatchDescription struct {
	payload    json.RawMessage
	receivedAt time.Time
}

type matchRecorder struct {
	outputDir   string
	description *pendingMatchDescription
	file        *os.File
	encoder     *json.Encoder
}

func newMatchRecorder(outputDir string) (*matchRecorder, error) {
	if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
		return nil, err
	}
	absOutputDir, err := filepath.Abs(outputDir)
	if err != nil {
		return nil, err
	}
	return &matchRecorder{outputDir: absOutputDir}, nil
}

func decodePayload(message []byte) (map[string]json.RawMessage, bool) {
	payload := map[string]json.RawMessage{}
	if err := json.Unmarshal(message, &payload); err != nil {
		return nil, false
	}
	return payload, true
}

func hasPayloadFields(payload map[string]json.RawMessage, fields ...string) bool {
	for _, field := range fields {
		if _, ok := payload[field]; !ok {
			return false
		}
	}
	return true
}

func isMatchDescription(payload map[string]json.RawMessage) bool {
	return hasPayloadFields(payload, "players", "seat_list", "game_config")
}

func isNewRound(payload map[string]json.RawMessage) bool {
	if !hasPayloadFields(payload, "tiles", "scores", "left_tile_count", "chang", "ju") {
		return false
	}
	_, hasSHA256 := payload["sha256"]
	_, hasMD5 := payload["md5"]
	return hasSHA256 || hasMD5
}

func isMatchEnd(payload map[string]json.RawMessage) bool {
	raw, ok := payload["gameend"]
	if !ok {
		return false
	}
	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case map[string]interface{}:
		return len(v) > 0
	case []interface{}:
		return len(v) > 0
	case string:
		return v != ""
	case float64:
		return v != 0
	default:
		return false
	}
}

func matchID(description map[string]json.RawMessage, startedAt time.Time) (string, error) {
	var seats []json.RawMessage
	if err := json.Unmarshal(description["seat_list"], &seats); err != nil {
		return "", err
	}
	seatIDs := make([]string, len(seats))
	for i, seat := range seats {
		seatIDs[i] = string(seat)
	}
	source := strings.Join(seatIDs, ",") + "|" + startedAt.Format("2006-01-02T15:04:05.000000Z07:00")
	sum := sha256.Sum256([]byte(source))
	id := binary.BigEndian.Uint64(sum[:8]) % 1000000000000
	return fmt.Sprintf("%012d", id), nil
}

func (r *matchRecorder) openMatch() error {
	description, ok := decodePayload(r.description.payload)
	if !ok {
		return fmt.Errorf("invalid match description")
	}
	id, err := matchID(description, r.description.receivedAt)
	if err != nil {
		return err
	}
	fileName := fmt.Sprintf("%s_%s.jsonl", r.description.receivedAt.Format("20060102_1504"), id)
	file, err := os.OpenFile(filepath.Join(r.outputDir, fileName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	r.file = file
	r.encoder = json.NewEncoder(file)
	r.encoder.SetEscapeHTML(false)
	return r.write(r.description.payload, r.description.receivedAt)
}

func (r *matchRecorder) write(payload json.RawMessage, receivedAt time.Time) error {
	return r.encoder.Encode(recordedPayload{
		Type:    recordTypeWebSocket,
		Time:    receivedAt.UnixNano() / int64(time.Millisecond),
		Payload: payload,
	})
}

func (r *matchRecorder) accept(message []byte, receivedAt time.Time) error {
	payload, ok := decodePayload(message)
	if !ok {
		return nil
	}

	if isMatchDescription(payload) {
		if err := r.closeMatch(); err != nil {
			return err
		}
		r.description = &pendingMatchDescription{
			payload:    append(json.RawMessage(nil), message...),
			receivedAt: receivedAt,
		}
		return nil
	}

	if r.description == nil {
		return nil
	}
	if r.file == nil {
		if !isNewRound(payload) {
			return nil
		}
		if err := r.openMatch(); err != nil {
			return err
		}
	}

	if err := r.write(json.RawMessage(message), receivedAt); err != nil {
		return err
	}
	if isMatchEnd(payload) {
		if err := r.closeMatch(); err != nil {
			return err
		}
		r.description = nil
	}
	return nil
}

func (r *matchRecorder) closeMatch() error {
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	r.encoder = nil
	return err
}

func (r *matchRecorder) close() error {
	return r.closeMatch()
}
