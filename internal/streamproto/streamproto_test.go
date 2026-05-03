package streamproto

import "testing"

func TestOpenAISSE_Fields(t *testing.T) {
	p := OpenAISSE()
	if p.Name != "openai_sse" {
		t.Errorf("Name = %q, want openai_sse", p.Name)
	}
	if !p.UsesSSE {
		t.Error("UsesSSE should be true")
	}
	if p.EventBoundary != '\n' {
		t.Errorf("EventBoundary = %q, want \\n", string(p.EventBoundary))
	}
	if p.CompletionMarker != "[DONE]" {
		t.Errorf("CompletionMarker = %q, want [DONE]", p.CompletionMarker)
	}
	if p.HasEventField {
		t.Error("HasEventField should be false")
	}
	if p.MaxLineSize != 256*1024 {
		t.Errorf("MaxLineSize = %d, want %d", p.MaxLineSize, 256*1024)
	}
}

func TestNone_Fields(t *testing.T) {
	p := None()
	if p.Name != "none" {
		t.Errorf("Name = %q, want none", p.Name)
	}
	if p.UsesSSE {
		t.Error("UsesSSE should be false")
	}
	if p.EventBoundary != 0 {
		t.Errorf("EventBoundary = %d, want 0", p.EventBoundary)
	}
	if p.CompletionMarker != "" {
		t.Errorf("CompletionMarker = %q, want empty", p.CompletionMarker)
	}
}

func TestIsStreaming(t *testing.T) {
	if !OpenAISSE().IsStreaming() {
		t.Error("OpenAISSE should be streaming")
	}
	if None().IsStreaming() {
		t.Error("None should not be streaming")
	}
}
