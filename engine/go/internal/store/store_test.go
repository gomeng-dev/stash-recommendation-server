package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gomeng-dev/stash-web-sprite-similarity-lab/engine/go/internal/scoring"
)

func TestResolveDBPathUsesPluginDataDirectory(t *testing.T) {
	got, err := ResolveDBPath("/tmp/StashHybridRecommendationsEngine", nil)
	if err != nil {
		t.Fatalf("ResolveDBPath returned error: %v", err)
	}
	want := filepath.Join("/tmp/StashHybridRecommendationsEngine", "data", "recommendations.sqlite")
	if got != want {
		t.Fatalf("db path mismatch: got %q want %q", got, want)
	}
}

func TestResolveDBPathFallsBackToExecutableDirectory(t *testing.T) {
	got, err := resolveDBPath("", nil, filepath.Join("/tmp", "StashHybridRecommendationsEngine", "stash-hybrid-engine"))
	if err != nil {
		t.Fatalf("resolveDBPath returned error: %v", err)
	}
	want := filepath.Join("/tmp", "StashHybridRecommendationsEngine", "data", "recommendations.sqlite")
	if got != want {
		t.Fatalf("db path mismatch: got %q want %q", got, want)
	}
}

func TestResolveDBPathStillErrorsWithoutPluginDirOrExecutable(t *testing.T) {
	if _, err := resolveDBPath("", nil, ""); err == nil {
		t.Fatal("expected error without PluginDir or executable fallback")
	}
}

func TestResolveDBPathRequiresExplicitTestOverride(t *testing.T) {
	_, err := ResolveDBPath("/tmp/plugin", map[string]any{"dbPath": "/tmp/override.sqlite"})
	if err != ErrDBPathOverrideNotAllowed {
		t.Fatalf("expected ErrDBPathOverrideNotAllowed, got %v", err)
	}
	got, err := ResolveDBPath("/tmp/plugin", map[string]any{"dbPath": "/tmp/override.sqlite", "allowTestDBPath": true})
	if err != nil {
		t.Fatalf("override returned error: %v", err)
	}
	if got != "/tmp/override.sqlite" {
		t.Fatalf("override mismatch: %q", got)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "recommendations.sqlite")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatalf("first Migrate returned error: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("second Migrate returned error: %v", err)
	}
	version, err := s.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion returned error: %v", err)
	}
	if version != CurrentSchemaVersion {
		t.Fatalf("schema version mismatch: got %d want %d", version, CurrentSchemaVersion)
	}
	count, err := s.SceneCount()
	if err != nil {
		t.Fatalf("SceneCount returned error: %v", err)
	}
	if count != 0 {
		t.Fatalf("new DB should have zero scenes, got %d", count)
	}
}

func TestMigrateAddsStashUpdatedAtToExistingScenesTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "recommendations.sqlite")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`CREATE TABLE scenes (
		id TEXT PRIMARY KEY,
		title TEXT,
		file_name TEXT,
		duration REAL,
		width INTEGER,
		height INTEGER,
		tags_json TEXT NOT NULL DEFAULT '[]',
		rating100 INTEGER,
		play_count INTEGER,
		thumbnail_url TEXT,
		screenshot_url TEXT,
		sprite_image_url TEXT,
		sprite_vtt_url TEXT,
		phash TEXT,
		updated_at TEXT NOT NULL
	);`); err != nil {
		t.Fatalf("create legacy scenes table: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate should handle legacy scenes table: %v", err)
	}
	scenes := []scoring.Scene{{ID: "scene-1", Title: "Scene", StashUpdatedAt: "2026-05-07T10:00:00Z"}}
	first, err := s.UpsertScenesIncremental(context.Background(), scenes)
	if err != nil {
		t.Fatalf("UpsertScenesIncremental returned error: %v", err)
	}
	second, err := s.UpsertScenesIncremental(context.Background(), scenes)
	if err != nil {
		t.Fatalf("second UpsertScenesIncremental returned error: %v", err)
	}
	if first.Inserted != 1 || second.Skipped != 1 {
		t.Fatalf("incremental summary mismatch after migration: first=%#v second=%#v", first, second)
	}
}

func TestUpsertScenesBuildsAndReadsRecommendationCacheWithoutPHashLeak(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "recommendations.sqlite")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	scenes := []scoring.Scene{
		{ID: "source", Title: "Source", FileName: "alpha beta.mp4", Tags: []string{"tag-a", "tag-b"}, DurationSeconds: 100, Width: 1920, Height: 1080, ThumbnailURL: "http://stash/scene/source/screenshot", PHash: "0000000000000000", StashUpdatedAt: "2026-05-07T10:00:00Z"},
		{ID: "candidate", Title: "Candidate", FileName: "alpha gamma.mp4", Tags: []string{"tag-a"}, DurationSeconds: 98, Width: 1920, Height: 1080, ThumbnailURL: "http://stash/scene/candidate/screenshot", PHash: "0000000000000003", StashUpdatedAt: "2026-05-07T10:00:00Z"},
		{ID: "far", Title: "Far", FileName: "unrelated.mp4", Tags: []string{"other"}, DurationSeconds: 10, Width: 640, Height: 360, PHash: "ffffffffffffffff", StashUpdatedAt: "2026-05-07T10:00:00Z"},
	}
	upsertSummary, err := s.UpsertScenesIncremental(ctx, scenes)
	if err != nil {
		t.Fatalf("UpsertScenes returned error: %v", err)
	}
	if upsertSummary.Inserted != 3 || upsertSummary.Updated != 0 || upsertSummary.Skipped != 0 || len(upsertSummary.ChangedIDs) != 3 {
		t.Fatalf("unexpected first upsert summary: %#v", upsertSummary)
	}
	again, err := s.UpsertScenesIncremental(ctx, scenes)
	if err != nil {
		t.Fatalf("second UpsertScenes returned error: %v", err)
	}
	if again.Inserted != 0 || again.Updated != 0 || again.Skipped != 3 || len(again.ChangedIDs) != 0 {
		t.Fatalf("unchanged scenes should be skipped: %#v", again)
	}
	scenes[1].Title = "Candidate Updated"
	scenes[1].StashUpdatedAt = "2026-05-07T11:00:00Z"
	changed, err := s.UpsertScenesIncremental(ctx, scenes)
	if err != nil {
		t.Fatalf("changed UpsertScenes returned error: %v", err)
	}
	if changed.Inserted != 0 || changed.Updated != 1 || changed.Skipped != 2 || len(changed.ChangedIDs) != 1 || changed.ChangedIDs[0] != "candidate" {
		t.Fatalf("only changed scene should be upserted: %#v", changed)
	}
	count, err := s.SceneCount()
	if err != nil || count != 3 {
		t.Fatalf("SceneCount mismatch: count=%d err=%v", count, err)
	}
	summary, err := s.BuildRecommendationCache(ctx, scoring.ModelVersionHybridV3Lite, 2, 20)
	if err != nil {
		t.Fatalf("BuildRecommendationCache returned error: %v", err)
	}
	if summary.Sources != 3 || summary.Rows == 0 {
		t.Fatalf("unexpected build summary: %#v", summary)
	}
	cacheStatuses, err := s.RecommendationCacheStatuses(ctx)
	if err != nil {
		t.Fatalf("RecommendationCacheStatuses returned error: %v", err)
	}
	if len(cacheStatuses) != 1 || cacheStatuses[0].ModelVersion != scoring.ModelVersionHybridV3Lite || cacheStatuses[0].Sources != 2 || cacheStatuses[0].Rows != summary.Rows || cacheStatuses[0].UpdatedAt == "" {
		t.Fatalf("unexpected cache statuses: %#v", cacheStatuses)
	}
	recs, err := s.Recommendations(ctx, scoring.ModelVersionHybridV3Lite, "source", 2)
	if err != nil {
		t.Fatalf("Recommendations returned error: %v", err)
	}
	if len(recs) == 0 || recs[0].SceneID != "candidate" {
		t.Fatalf("unexpected recommendations: %#v", recs)
	}
	if recs[0].Scene.ThumbnailURL == "" {
		t.Fatalf("thumbnail URL should be present: %#v", recs[0])
	}
	if recs[0].Scene.ID != "candidate" || recs[0].Scene.Title == "" {
		t.Fatalf("public scene DTO missing fields: %#v", recs[0].Scene)
	}
	cachedSources, err := s.sourcesWithCachedCandidates(ctx, map[string]struct{}{"candidate": {}})
	if err != nil {
		t.Fatalf("sourcesWithCachedCandidates returned error: %v", err)
	}
	if !containsString(cachedSources, "source") {
		t.Fatalf("changed cached candidate should include existing cache source for stale-row cleanup: %#v", cachedSources)
	}
	impacted, err := s.ImpactedRecommendationSourceIDs(ctx, []string{"candidate"})
	if err != nil {
		t.Fatalf("ImpactedRecommendationSourceIDs returned error: %v", err)
	}
	if len(impacted) == 0 || !containsString(impacted, "candidate") || !containsString(impacted, "source") {
		t.Fatalf("candidate change should impact itself and related source: %#v", impacted)
	}
	partial, err := s.BuildRecommendationCacheForSourcesWithProgress(ctx, scoring.ModelVersionHybridV3Lite, impacted, 2, 20, nil)
	if err != nil {
		t.Fatalf("BuildRecommendationCacheForSourcesWithProgress returned error: %v", err)
	}
	if !partial.Partial || partial.Sources != len(impacted) || partial.Rows == 0 {
		t.Fatalf("unexpected partial build summary: %#v impacted=%#v", partial, impacted)
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
