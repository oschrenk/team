package inspect

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

// LoggedMessage is one parsed record from messages.log.
type LoggedMessage struct {
	Ts          string `json:"ts"`
	MsgID       string `json:"msg_id"`
	Kind        string `json:"kind"`
	From        string `json:"from"`
	FromName    string `json:"from_name"`
	FromLabel   string `json:"from_label,omitempty"`
	To          string `json:"to,omitempty"`
	ToSessionID string `json:"to_session_id,omitempty"`
	Text        string `json:"text"`
}

// TailJSONL returns the last n parsed records from a JSONL file at
// path. Missing file → empty slice (no error). Malformed lines are
// skipped silently.
//
// Uses a circular ring buffer so each line costs O(1) and we never
// materialize the entire file.
func TailJSONL(path string, n int) ([]LoggedMessage, error) {
	if n <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []LoggedMessage{}, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// Allow up to 16 MB per line (messages can be large; bus enforces caps).
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)

	ring := make([]LoggedMessage, n)
	idx := 0
	filled := 0
	for sc.Scan() {
		var m LoggedMessage
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue // skip malformed line
		}
		ring[idx] = m
		idx = (idx + 1) % n
		if filled < n {
			filled++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	out := make([]LoggedMessage, 0, filled)
	start := idx - filled
	if start < 0 {
		start += n
	}
	for i := 0; i < filled; i++ {
		out = append(out, ring[(start+i)%n])
	}
	return out, nil
}
