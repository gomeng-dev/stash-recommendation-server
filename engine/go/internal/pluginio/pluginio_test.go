package pluginio

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReadInputAcceptsStashCapitalizedKeys(t *testing.T) {
	input, err := ReadInput(strings.NewReader(`{"Args":{"mode":"recommend","limit":3},"PluginDir":"/tmp/plugin"}`))
	if err != nil {
		t.Fatalf("ReadInput returned error: %v", err)
	}
	if input.PluginDir != "/tmp/plugin" {
		t.Fatalf("PluginDir mismatch: %q", input.PluginDir)
	}
	if input.Args["mode"] != "recommend" {
		t.Fatalf("mode mismatch: %#v", input.Args)
	}
	if input.Args["limit"].(float64) != 3 {
		t.Fatalf("limit mismatch: %#v", input.Args["limit"])
	}
}

func TestMarshalOutputWrapsJSONString(t *testing.T) {
	data, err := MarshalOutput(map[string]any{"ok": true, "mode": "status"})
	if err != nil {
		t.Fatalf("MarshalOutput returned error: %v", err)
	}
	var envelope OutputEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("outer JSON invalid: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(envelope.Output), &payload); err != nil {
		t.Fatalf("inner JSON invalid: %v", err)
	}
	if payload["ok"] != true || payload["mode"] != "status" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestReadInputUsesServerConnectionPluginDirFallback(t *testing.T) {
	input, err := ReadInput(strings.NewReader(`{"ServerConnection":{"PluginDir":"/tmp/plugin-from-connection"},"Args":{"mode":"status"}}`))
	if err != nil {
		t.Fatalf("ReadInput returned error: %v", err)
	}
	if input.PluginDir != "/tmp/plugin-from-connection" {
		t.Fatalf("PluginDir mismatch: %q", input.PluginDir)
	}
}
