package httpjson

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeRejectsTrailingJSON(t *testing.T) {
	body := `{"slug":"a"}{"extra":true}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var dst map[string]any
	if err := Decode(req, &dst); err == nil {
		t.Fatal("expected trailing json error")
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	body := `{"slug":"a","unknown":1}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var dst struct {
		Slug string `json:"slug"`
	}
	if err := Decode(req, &dst); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestDecodeRejectsOversizedBody(t *testing.T) {
	body := `{"slug":"` + strings.Repeat("a", MaxBodyBytes) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var dst map[string]any
	if err := Decode(req, &dst); err == nil {
		t.Fatal("expected body too large")
	}
}

func TestDecodeAcceptsValid(t *testing.T) {
	body := `{"slug":"ok"}`
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(body)))
	var dst struct {
		Slug string `json:"slug"`
	}
	if err := Decode(req, &dst); err != nil {
		t.Fatal(err)
	}
	if dst.Slug != "ok" {
		t.Fatalf("got %q", dst.Slug)
	}
}
