package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusCreated, map[string]any{"hello": "world"})

	if got, want := rec.Code, http.StatusCreated; got != want {
		t.Fatalf("status: got %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), "application/json"; got != want {
		t.Fatalf("content-type: got %q, want %q", got, want)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["hello"] != "world" {
		t.Fatalf("body: got %v", body)
	}
}

func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	t.Run("valid", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"alice"}`))
		var p payload
		if err := decodeJSON(r, &p); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if p.Name != "alice" {
			t.Fatalf("got %q, want %q", p.Name, "alice")
		}
	})

	t.Run("unknown field rejected", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"alice","extra":1}`))
		var p payload
		if err := decodeJSON(r, &p); err == nil {
			t.Fatal("expected error for unknown field, got nil")
		}
	})

	t.Run("empty body", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(nil))
		var p payload
		if err := decodeJSON(r, &p); err == nil {
			t.Fatal("expected error for empty body, got nil")
		}
	})
}

func TestDecodeJSONNulls(t *testing.T) {
	type payload struct {
		Name  *string `json:"name"`
		Other *string `json:"other"`
	}
	r := httptest.NewRequest(http.MethodPatch, "/x", strings.NewReader(`{"name":null,"other":"set"}`))
	var p payload
	nulls, err := decodeJSONNulls(r, &p)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !nulls["name"] || nulls["other"] || len(nulls) != 1 || p.Name != nil || p.Other == nil {
		t.Fatalf("nulls = %v payload = %+v, want only name null", nulls, p)
	}

	for name, body := range map[string]string{
		"unknown field": `{"name":null,"extra":1}`,
		"empty body":    ``,
		"oversized":     `{"name":"` + strings.Repeat("a", maxJSONBodyBytes) + `"}`,
	} {
		if _, err := decodeJSONNulls(httptest.NewRequest(http.MethodPatch, "/x", strings.NewReader(body)), &p); err == nil {
			t.Fatalf("%s: expected error, got nil", name)
		}
	}
	noBody := httptest.NewRequest(http.MethodPatch, "/x", nil)
	noBody.Body = nil
	if _, err := decodeJSONNulls(noBody, &p); err == nil {
		t.Fatal("nil body: expected error, got nil")
	}
	if nulls := jsonNullFields(nil); len(nulls) != 0 {
		t.Fatalf("jsonNullFields(nil) = %v, want none", nulls)
	}
}

// A JSON body is read only up to maxJSONBodyBytes; past that the decode fails
// instead of streaming the rest into memory.
func TestDecodeJSONBoundsTheBody(t *testing.T) {
	var v map[string]string
	within := `{"k":"` + strings.Repeat("a", 1000) + `"}`
	if err := decodeJSON(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(within)), &v); err != nil {
		t.Fatalf("small body: %v", err)
	}
	oversized := `{"k":"` + strings.Repeat("a", maxJSONBodyBytes) + `"}`
	if err := decodeJSON(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(oversized)), &v); err == nil {
		t.Fatal("an oversized body decoded")
	}
}
