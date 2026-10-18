package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// maxJSONBodyBytes bounds a JSON request body. The largest legitimate one is
// a 100,000-character description or context body, well inside this, and a
// cap keeps a caller from streaming an unbounded body into the decoder.
const maxJSONBodyBytes = 2 << 20

func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("empty body")
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxJSONBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("empty body")
		}
		return err
	}
	return nil
}

// decodeJSONNulls is decodeJSON for an update that can clear fields. Decoding
// reads a field sent as null and one left out alike, so it also returns the
// top-level fields the body sets to null.
func decodeJSONNulls(r *http.Request, v any) (map[string]bool, error) {
	if r.Body == nil {
		return nil, errors.New("empty body")
	}
	data, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxJSONBodyBytes))
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	if err := decodeJSON(r, v); err != nil {
		return nil, err
	}
	return jsonNullFields(data), nil
}

// jsonNullFields lists the top-level fields a JSON object sets to null.
func jsonNullFields(data []byte) map[string]bool {
	var fields map[string]json.RawMessage
	_ = json.NewDecoder(bytes.NewReader(data)).Decode(&fields) // the caller has decoded it already
	nulls := map[string]bool{}
	for name, value := range fields {
		if string(value) == "null" {
			nulls[name] = true
		}
	}
	return nulls
}
