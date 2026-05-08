package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchAllScenesMapsScreenshotAndNormalizedPHash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("ApiKey"); got != "secret" {
			t.Fatalf("ApiKey header mismatch: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"findScenes": map[string]any{
					"count": 1,
					"scenes": []map[string]any{{
						"id":         "scene-1",
						"title":      "Scene 1",
						"updated_at": "2026-05-07T10:00:00Z",
						"rating100":  80,
						"play_count": 3,
						"files": []map[string]any{{
							"basename":    "Alpha_Beta_1080p.mp4",
							"duration":    120,
							"width":       1920,
							"height":      1080,
							"fingerprint": "0xABCDEF0123456789",
						}},
						"tags":  []map[string]any{{"name": "Tag A"}},
						"paths": map[string]any{"screenshot": "http://stash/scene/1/screenshot", "sprite": "http://stash/scene/1/sprite.jpg", "vtt": "http://stash/scene/1/sprite.vtt"},
					}},
				},
			},
		})
	}))
	defer server.Close()

	client := Client{URL: server.URL, APIKey: "secret"}
	scenes, err := client.FetchAllScenes(context.Background(), 0, 500)
	if err != nil {
		t.Fatalf("FetchAllScenes returned error: %v", err)
	}
	if len(scenes) != 1 {
		t.Fatalf("scene count mismatch: %#v", scenes)
	}
	if scenes[0].PHash != "abcdef0123456789" || scenes[0].ThumbnailURL == "" || scenes[0].Tags[0] != "Tag A" || scenes[0].StashUpdatedAt != "2026-05-07T10:00:00Z" {
		t.Fatalf("mapped scene mismatch: %#v", scenes[0])
	}
}

func TestFetchAllScenesReturnsSchemaErrorsWithoutLegacyRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]any{{"message": "Cannot query field screenshot"}}})
	}))
	defer server.Close()

	client := Client{URL: server.URL}
	_, err := client.FetchAllScenes(context.Background(), 0, 500)
	if err == nil {
		t.Fatal("expected schema error")
	}
	if got := err.Error(); got != "Stash GraphQL error: Cannot query field screenshot" {
		t.Fatalf("unexpected error: %v", got)
	}
	if calls != 1 {
		t.Fatalf("expected one query without retry, got %d calls", calls)
	}
}
