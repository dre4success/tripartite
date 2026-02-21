package adapter

import "testing"

func TestGeminiParseResponseWrapperWithSessionID(t *testing.T) {
	g := &Gemini{}
	out := `{"session_id":"abc","response":"wrapped response"}`

	got, err := g.ParseResponse([]byte(out))
	if err != nil {
		t.Fatalf("ParseResponse() error = %v", err)
	}
	if got != "wrapped response" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestGeminiParseResponseWrapperResponseOnly(t *testing.T) {
	g := &Gemini{}
	out := `{"response":"only response"}`

	got, err := g.ParseResponse([]byte(out))
	if err != nil {
		t.Fatalf("ParseResponse() error = %v", err)
	}
	if got != "only response" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestGeminiParseResponseDirectJSONResult(t *testing.T) {
	g := &Gemini{}
	out := `{"result":"direct result"}`

	got, err := g.ParseResponse([]byte(out))
	if err != nil {
		t.Fatalf("ParseResponse() error = %v", err)
	}
	if got != "direct result" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestGeminiParseResponseJSONLMessages(t *testing.T) {
	g := &Gemini{}
	out := "{\"type\":\"message\",\"content\":\"line1\"}\n{\"type\":\"result\",\"content\":\"line2\"}"

	got, err := g.ParseResponse([]byte(out))
	if err != nil {
		t.Fatalf("ParseResponse() error = %v", err)
	}
	if got != "line1\n\nline2" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestGeminiParseResponseMalformedFallsBackRaw(t *testing.T) {
	g := &Gemini{}
	out := "{invalid json"

	got, err := g.ParseResponse([]byte(out))
	if err != nil {
		t.Fatalf("ParseResponse() error = %v", err)
	}
	if got != out {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestGeminiParseResponseEmpty(t *testing.T) {
	g := &Gemini{}
	_, err := g.ParseResponse(nil)
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}
