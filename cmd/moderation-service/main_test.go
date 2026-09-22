package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPValidation(t *testing.T) {
	a := app{policyPath: "../../config/policy.json"}
	for _, body := range []string{`{}`, `{"text":" "}`, `{"text":"ok","type":"unknown"}`, `{"text":"ok","extra":1}`, `{"text":"ok"} {}`} {
		w := httptest.NewRecorder()
		a.moderate(w, httptest.NewRequest("POST", "/v1/moderate", strings.NewReader(body)))
		if w.Code != 400 {
			t.Errorf("%s: got %d", body, w.Code)
		}
	}
	w := httptest.NewRecorder()
	a.moderate(w, httptest.NewRequest("POST", "/v1/moderate", strings.NewReader(`{"text":"`+strings.Repeat("a", 1<<20)+`"}`)))
	if w.Code != 413 {
		t.Fatalf("oversize: %d", w.Code)
	}
}
func TestAuthorization(t *testing.T) {
	a := app{token: "secret"}
	handler := a.authorize(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	for _, token := range []string{"", "Bearer wrong", "Bearer secret"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", token)
		w := httptest.NewRecorder()
		handler(w, r)
		want := 401
		if token == "Bearer secret" {
			want = 204
		}
		if w.Code != want {
			t.Fatal(w.Code)
		}
	}
}
