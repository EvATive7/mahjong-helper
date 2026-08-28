package main

import (
	"bufio"
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func testPayload(value string) []byte {
	return []byte(value)
}

func readRecordedPayloads(t *testing.T, path string) []recordedPayload {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var entries []recordedPayload
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := map[string]json.RawMessage{}
		if err := json.Unmarshal(scanner.Bytes(), &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 3 || fields["type"] == nil || fields["time"] == nil || fields["payload"] == nil {
			t.Fatalf("unexpected JSONL fields: %v", fields)
		}
		var entry recordedPayload
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestMatchRecorderWritesWrappedMatchPayloads(t *testing.T) {
	dir, err := ioutil.TempDir("", "mahjong-helper-recorder-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	recorder, err := newMatchRecorder(dir)
	if err != nil {
		t.Fatal(err)
	}
	descriptionTime := time.Date(2026, 8, 29, 10, 30, 1, 123000000, time.FixedZone("CST", 8*60*60))
	description := testPayload(`{"players":[{"account_id":30},{"account_id":10},{"account_id":20}],"seat_list":[10,20,30],"game_config":{"meta":{"mode_id":21}},"is_game_start":false}`)
	ready := testPayload(`{"ready_id_list":[10,20,30]}`)
	newRound := testPayload(`{"tiles":["1m","2m"],"scores":[35000,35000,35000],"left_tile_count":54,"sha256":"abc","chang":0,"ju":0}`)
	discard := testPayload(`{"seat":0,"tile":"1m","moqie":true}`)

	if err := recorder.accept(testPayload(`{"account_id":10}`), descriptionTime.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := recorder.accept(description, descriptionTime); err != nil {
		t.Fatal(err)
	}
	if err := recorder.accept(ready, descriptionTime.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if files, _ := ioutil.ReadDir(dir); len(files) != 0 {
		t.Fatalf("file created before first round: %v", files)
	}
	if err := recorder.accept(newRound, descriptionTime.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := recorder.accept(discard, descriptionTime.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := recorder.close(); err != nil {
		t.Fatal(err)
	}

	files, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	if !regexp.MustCompile(`^20260829_1030_[0-9]{12}\.jsonl$`).MatchString(files[0].Name()) {
		t.Fatalf("unexpected file name: %s", files[0].Name())
	}
	if files[0].Name() != "20260829_1030_124600766421.jsonl" {
		t.Fatalf("file name = %s, ID generation differs from mahjong-recorder", files[0].Name())
	}

	entries := readRecordedPayloads(t, filepath.Join(dir, files[0].Name()))
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	wantPayloads := [][]byte{description, newRound, discard}
	wantTimes := []int64{
		descriptionTime.UnixNano() / int64(time.Millisecond),
		descriptionTime.Add(2*time.Second).UnixNano() / int64(time.Millisecond),
		descriptionTime.Add(3*time.Second).UnixNano() / int64(time.Millisecond),
	}
	for i, entry := range entries {
		if entry.Type != recordTypeWebSocket {
			t.Errorf("entry %d type = %q", i, entry.Type)
		}
		if entry.Time != wantTimes[i] {
			t.Errorf("entry %d time = %d, want %d", i, entry.Time, wantTimes[i])
		}
		if string(entry.Payload) != string(wantPayloads[i]) {
			t.Errorf("entry %d payload = %s, want %s", i, entry.Payload, wantPayloads[i])
		}
	}
}

func TestMatchRecorderStopsAfterMatchEnd(t *testing.T) {
	dir, err := ioutil.TempDir("", "mahjong-helper-recorder-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	recorder, err := newMatchRecorder(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 29, 10, 30, 0, 0, time.Local)
	messages := [][]byte{
		testPayload(`{"players":[],"seat_list":[1,2,3],"game_config":{}}`),
		testPayload(`{"tiles":[],"scores":[],"left_tile_count":54,"md5":"abc","chang":0,"ju":0}`),
		testPayload(`{"hules":[],"gameend":{"scores":[25000,25000,25000]}}`),
		testPayload(`{"seat":0,"tile":"9m"}`),
	}
	for i, message := range messages {
		if err := recorder.accept(message, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.close(); err != nil {
		t.Fatal(err)
	}

	files, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries := readRecordedPayloads(t, filepath.Join(dir, files[0].Name()))
	if len(entries) != 3 {
		t.Fatalf("got %d entries after match end, want 3", len(entries))
	}
}
