package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gomeng-dev/stash-web-sprite-similarity-lab/engine/go/internal/pluginio"
	"github.com/gomeng-dev/stash-web-sprite-similarity-lab/engine/go/internal/scoring"
	"github.com/gomeng-dev/stash-web-sprite-similarity-lab/engine/go/internal/store"
)

func TestStatusWithoutPluginDirUsesExecutableDirectoryFallback(t *testing.T) {
	payload := Run(pluginio.Input{Args: map[string]any{"mode": "status"}})
	if payload["ok"] != true || payload["mode"] != "status" {
		t.Fatalf("unexpected status payload: %#v", payload)
	}
	capabilities := payload["capabilities"].([]string)
	if !containsString(capabilities, "index-scenes") || !containsString(capabilities, "build-cache") || !containsString(capabilities, "import-db") {
		t.Fatalf("status capabilities should expose split index/cache modes: %#v", capabilities)
	}
	database := payload["database"].(map[string]any)
	if database["configured"] != true || database["exists"] != false {
		t.Fatalf("expected configured but missing database via executable fallback, got %#v", database)
	}
}

func TestBootstrapMigratesPluginDataDB(t *testing.T) {
	pluginDir := t.TempDir()
	payload := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "bootstrap"}})
	if payload["ok"] != true || payload["bootstrapped"] != true {
		t.Fatalf("unexpected bootstrap payload: %#v", payload)
	}
	database := payload["database"].(map[string]any)
	if int(database["schemaVersion"].(int)) != store.CurrentSchemaVersion {
		t.Fatalf("schema version mismatch: %#v", database)
	}
	// Idempotency through the public engine mode.
	again := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "bootstrap"}})
	if again["ok"] != true {
		t.Fatalf("second bootstrap failed: %#v", again)
	}
	if _, err := filepath.Abs(filepath.Join(pluginDir, "data", "recommendations.sqlite")); err != nil {
		t.Fatalf("test path invalid: %v", err)
	}
}

func TestRecommendReturnsCacheMissForMissingOrEmptyDB(t *testing.T) {
	pluginDir := t.TempDir()
	missing := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "recommend", "sceneId": "scene-1", "limit": float64(3)}})
	if missing["ok"] != true || missing["cacheHit"] != false || missing["fallbackReason"] != "engine-db-missing" {
		t.Fatalf("unexpected missing-db recommend payload: %#v", missing)
	}

	boot := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "bootstrap"}})
	if boot["ok"] != true {
		t.Fatalf("bootstrap failed: %#v", boot)
	}
	empty := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "recommend", "sceneId": "scene-1", "limit": float64(3)}})
	if empty["ok"] != true || empty["cacheHit"] != false || empty["fallbackReason"] != "cache-miss" {
		t.Fatalf("unexpected empty-db recommend payload: %#v", empty)
	}
	switch got := empty["recommendations"].(type) {
	case []store.Recommendation:
		if len(got) != 0 {
			t.Fatalf("expected zero recommendations, got %#v", got)
		}
	case []any:
		if len(got) != 0 {
			t.Fatalf("expected zero recommendations, got %#v", got)
		}
	default:
		t.Fatalf("unexpected recommendations type: %#v", empty["recommendations"])
	}
}

func TestImportDBBacksUpCurrentDBAndMigratesImportedFile(t *testing.T) {
	ctx := t.Context()
	sourcePath := filepath.Join(t.TempDir(), "source.sqlite")
	source, err := store.Open(sourcePath)
	if err != nil {
		t.Fatalf("open source DB: %v", err)
	}
	if err := source.Migrate(); err != nil {
		t.Fatalf("migrate source DB: %v", err)
	}
	scenes := []scoring.Scene{
		{ID: "source", Title: "Source", FileName: "alpha beta.mp4", Tags: []string{"tag-a", "tag-b"}, DurationSeconds: 100, Width: 1920, Height: 1080, PHash: "0000000000000000", StashUpdatedAt: "2026-05-07T10:00:00Z"},
		{ID: "candidate", Title: "Candidate", FileName: "alpha gamma.mp4", Tags: []string{"tag-a"}, DurationSeconds: 99, Width: 1920, Height: 1080, PHash: "0000000000000003", StashUpdatedAt: "2026-05-07T10:00:00Z"},
	}
	if err := source.UpsertScenes(ctx, scenes); err != nil {
		t.Fatalf("seed source DB: %v", err)
	}
	if _, err := source.BuildRecommendationCache(ctx, DefaultModelVersion, 2, 20); err != nil {
		t.Fatalf("build source cache: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("close source DB: %v", err)
	}

	pluginDir := t.TempDir()
	boot := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "bootstrap"}})
	if boot["ok"] != true {
		t.Fatalf("bootstrap target DB failed: %#v", boot)
	}
	imported := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "import-db", "sourceDbPath": sourcePath}})
	if imported["ok"] != true || imported["imported"] != true || imported["sceneCount"] != 2 {
		t.Fatalf("unexpected import payload: %#v", imported)
	}
	if imported["previousDatabaseBackup"] == "" {
		t.Fatalf("import should back up existing DB: %#v", imported)
	}
	recommend := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "recommend", "sceneId": "source", "limit": float64(1)}})
	if recommend["ok"] != true || recommend["cacheHit"] != true {
		t.Fatalf("imported DB cache should be readable: %#v", recommend)
	}
}

func TestOutputPayloadIsJSONSerializable(t *testing.T) {
	payload := Run(pluginio.Input{Args: map[string]any{"mode": "index-scenes"}})
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("payload should be JSON serializable: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty JSON payload")
	}
}

func TestIndexScenesAndBuildCacheAreSeparateTasks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"findScenes": map[string]any{
					"count": 2,
					"scenes": []map[string]any{
						{
							"id": "source", "title": "Source", "updated_at": "2026-05-07T10:00:00Z",
							"files": []map[string]any{{"basename": "alpha beta.mp4", "duration": 100, "width": 1920, "height": 1080, "fingerprint": "0000000000000000"}},
							"tags":  []map[string]any{{"name": "tag-a"}, {"name": "tag-b"}},
							"paths": map[string]any{"screenshot": "http://stash/scene/source/screenshot"},
						},
						{
							"id": "candidate", "title": "Candidate", "updated_at": "2026-05-07T10:00:00Z",
							"files": []map[string]any{{"basename": "alpha gamma.mp4", "duration": 96, "width": 1920, "height": 1080, "fingerprint": "0000000000000003"}},
							"tags":  []map[string]any{{"name": "tag-a"}},
							"paths": map[string]any{"screenshot": "http://stash/scene/candidate/screenshot"},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	pluginDir := t.TempDir()
	indexPayload := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "index-scenes", "stashGraphqlUrl": server.URL, "limit": float64(1)}})
	if indexPayload["ok"] != true || indexPayload["indexedScenes"] != 2 || indexPayload["cacheBuilt"] != false {
		t.Fatalf("unexpected index payload: %#v", indexPayload)
	}
	if indexPayload["insertedScenes"] != 2 || indexPayload["updatedScenes"] != 0 || indexPayload["skippedScenes"] != 0 {
		t.Fatalf("unexpected first incremental index summary: %#v", indexPayload)
	}
	again := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "index-scenes", "stashGraphqlUrl": server.URL, "limit": float64(1)}})
	if again["ok"] != true || again["insertedScenes"] != 0 || again["updatedScenes"] != 0 || again["skippedScenes"] != 2 {
		t.Fatalf("unchanged scenes should be skipped: %#v", again)
	}
	miss := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "recommend", "sceneId": "source", "limit": float64(1)}})
	if miss["ok"] != true || miss["cacheHit"] != false || miss["fallbackReason"] != "cache-miss" {
		t.Fatalf("index-only task should not populate recommendation cache: %#v", miss)
	}
	cachePayload := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "build-cache", "limit": float64(1)}})
	if cachePayload["ok"] != true || cachePayload["mode"] != "build-cache" || cachePayload["cacheBuilt"] != true {
		t.Fatalf("unexpected cache build payload: %#v", cachePayload)
	}
	if cachePayload["cacheRows"].(int) == 0 || cachePayload["cacheSources"].(int) != 2 {
		t.Fatalf("cache build should produce rows for indexed scenes: %#v", cachePayload)
	}
	recommend := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "recommend", "sceneId": "source", "limit": float64(1)}})
	if recommend["ok"] != true || recommend["cacheHit"] != true {
		t.Fatalf("unexpected recommend payload: %#v", recommend)
	}
	recs := recommend["recommendations"].([]store.Recommendation)
	if len(recs) != 1 || recs[0].SceneID != "candidate" {
		t.Fatalf("unexpected recommendations: %#v", recs)
	}

	status := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "status"}})
	if status["ok"] != true || status["sceneCount"] != 2 {
		t.Fatalf("unexpected status payload: %#v", status)
	}
	database := status["database"].(map[string]any)
	if database["exists"] != true || database["schemaVersion"] != store.CurrentSchemaVersion {
		t.Fatalf("unexpected database status: %#v", database)
	}
	caches := status["recommendationCaches"].([]store.RecommendationCacheStatus)
	if len(caches) != 1 || caches[0].ModelVersion != DefaultModelVersion || caches[0].Sources != 2 || caches[0].Rows == 0 {
		t.Fatalf("unexpected cache statuses: %#v", caches)
	}
}

func TestIndexScenesUsesStashServerConnectionAndSessionCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("stash_session")
		if err != nil || cookie.Value != "ok" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]any{{"message": "missing cookie"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"findScenes": map[string]any{
					"count": 1,
					"scenes": []map[string]any{
						{
							"id": "source", "title": "Source", "updated_at": "2026-05-07T10:00:00Z",
							"files": []map[string]any{{"basename": "alpha beta.mp4", "duration": 100, "width": 1920, "height": 1080, "fingerprint": "0000000000000000"}},
							"tags":  []map[string]any{{"name": "tag-a"}},
							"paths": map[string]any{"screenshot": "http://stash/scene/source/screenshot"},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}

	payload := Run(pluginio.Input{
		PluginDir: t.TempDir(),
		Args:      map[string]any{"mode": "index-scenes", "limit": float64(1)},
		ServerConnection: map[string]any{
			"Scheme": parsed.Scheme,
			"Host":   parsed.Hostname(),
			"Port":   float64(port),
			"SessionCookie": map[string]any{
				"Name":  "stash_session",
				"Value": "ok",
			},
		},
	})
	if payload["ok"] != true || payload["indexedScenes"] != 1 || payload["cacheBuilt"] != false {
		t.Fatalf("unexpected server-connection index payload: %#v", payload)
	}
}

func TestPruneDeletedScenesRemovesStaleSceneWithoutFullRebuild(t *testing.T) {
	ctx := t.Context()
	pluginDir := t.TempDir()
	dbPath := filepath.Join(pluginDir, "data", "recommendations.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("create db dir: %v", err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open DB: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate DB: %v", err)
	}
	seed := []scoring.Scene{
		{ID: "source", Title: "Source", FileName: "alpha beta.mp4", Tags: []string{"tag-a", "tag-b"}, DurationSeconds: 100, Width: 1920, Height: 1080, PHash: "0000000000000000", StashUpdatedAt: "2026-05-07T10:00:00Z"},
		{ID: "candidate", Title: "Candidate", FileName: "alpha gamma.mp4", Tags: []string{"tag-a"}, DurationSeconds: 98, Width: 1920, Height: 1080, PHash: "0000000000000003", StashUpdatedAt: "2026-05-07T10:00:00Z"},
		{ID: "deleted", Title: "Deleted", FileName: "alpha beta deleted.mp4", Tags: []string{"tag-a", "tag-b"}, DurationSeconds: 101, Width: 1920, Height: 1080, PHash: "0000000000000001", StashUpdatedAt: "2026-05-07T10:00:00Z"},
	}
	if err := s.UpsertScenes(ctx, seed); err != nil {
		t.Fatalf("seed DB: %v", err)
	}
	if _, err := s.BuildRecommendationCache(ctx, DefaultModelVersion, 2, 20); err != nil {
		t.Fatalf("build initial cache: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close seeded DB: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"findScenes": map[string]any{
					"count": 2,
					"scenes": []map[string]any{
						{"id": "source"},
						{"id": "candidate"},
					},
				},
			},
		})
	}))
	defer server.Close()

	dryRun := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "prune-deleted-scenes", "stashGraphqlUrl": server.URL, "limit": float64(2), "dryRun": true}})
	if dryRun["ok"] != true || dryRun["deletedScenes"] != 1 || dryRun["dryRun"] != true || dryRun["cacheBuilt"] != false {
		t.Fatalf("unexpected dry-run prune payload: %#v", dryRun)
	}
	statusAfterDryRun := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "status"}})
	if statusAfterDryRun["sceneCount"] != 3 {
		t.Fatalf("dry-run should not mutate DB, got status: %#v", statusAfterDryRun)
	}

	payload := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "prune-deleted-scenes", "stashGraphqlUrl": server.URL, "limit": float64(2)}})
	if payload["ok"] != true || payload["mode"] != "prune-deleted-scenes" {
		t.Fatalf("unexpected prune payload: %#v", payload)
	}
	if payload["deletedScenes"] != 1 || payload["stashSceneCount"] != 2 || payload["cacheScope"] != "partial" {
		t.Fatalf("prune summary should report lightweight partial cleanup: %#v", payload)
	}

	status := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "status"}})
	if status["sceneCount"] != 2 {
		t.Fatalf("status should reflect pruned DB scene count: %#v", status)
	}
	recommend := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "recommend", "sceneId": "source", "limit": float64(2)}})
	if recommend["ok"] != true || recommend["cacheHit"] != true {
		t.Fatalf("recommend should still hit cache after partial prune: %#v", recommend)
	}
	recs := recommend["recommendations"].([]store.Recommendation)
	for _, rec := range recs {
		if rec.SceneID == "deleted" {
			t.Fatalf("deleted scene should not remain in recommendations: %#v", recs)
		}
	}
}

func TestPruneDeletedScenesRefusesBoundedStashFetch(t *testing.T) {
	payload := Run(pluginio.Input{PluginDir: t.TempDir(), Args: map[string]any{"mode": "prune-deleted-scenes", "stashGraphqlUrl": "http://127.0.0.1/graphql", "maxScenes": float64(10)}})
	if payload["ok"] != false || payload["fallbackReason"] != "bounded-prune-refused" {
		t.Fatalf("prune should refuse bounded Stash ID fetches: %#v", payload)
	}
}

func TestDevTest100BuildsFreshBoundedDatabaseAndReturnsVerificationScenes(t *testing.T) {
	scenes := make([]map[string]any, 0, 150)
	for i := 1; i <= 150; i++ {
		scenes = append(scenes, map[string]any{
			"id":         fmt.Sprintf("%d", i),
			"title":      fmt.Sprintf("Dev Test Scene %03d", i),
			"updated_at": fmt.Sprintf("2026-05-07T10:%02d:00Z", i%60),
			"files": []map[string]any{{
				"basename":    fmt.Sprintf("dev-test-alpha-%03d.mp4", i),
				"duration":    100 + float64(i%5),
				"width":       1920,
				"height":      1080,
				"fingerprint": fmt.Sprintf("%016x", i),
			}},
			"tags":  []map[string]any{{"name": "dev-test"}, {"name": fmt.Sprintf("cluster-%d", i%3)}},
			"paths": map[string]any{"screenshot": fmt.Sprintf("http://stash/scene/%d/screenshot", i)},
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"findScenes": map[string]any{
					"count":  150,
					"scenes": scenes,
				},
			},
		})
	}))
	defer server.Close()

	pluginDir := t.TempDir()
	first := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "dev-test-100", "stashGraphqlUrl": server.URL}})
	if first["ok"] != true || first["mode"] != "dev-test-100" || first["indexedScenes"] != 100 || first["cacheBuilt"] != true {
		t.Fatalf("unexpected first dev-test payload: %#v", first)
	}
	if first["maxScenes"] != 100 || first["developmentOnly"] != true {
		t.Fatalf("dev-test safety metadata missing: %#v", first)
	}
	if first["cacheRows"].(int) == 0 {
		t.Fatalf("dev-test cache should contain rows: %#v", first)
	}
	samples := first["verificationScenes"].([]store.RecommendationSourceSample)
	if len(samples) == 0 || samples[0].ID == "" || samples[0].RecommendationCount == 0 {
		t.Fatalf("expected verification scenes with recommendation counts: %#v", samples)
	}
	status := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "status"}})
	if status["sceneCount"] != 100 {
		t.Fatalf("dev-test DB should contain exactly 100 scenes, got %#v", status)
	}

	second := Run(pluginio.Input{PluginDir: pluginDir, Args: map[string]any{"mode": "dev-test-100", "stashGraphqlUrl": server.URL}})
	if second["ok"] != true || second["previousDatabaseBackup"] == "" {
		t.Fatalf("second dev-test run should move previous DB aside: %#v", second)
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
