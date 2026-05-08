package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gomeng-dev/stash-web-sprite-similarity-lab/engine/go/internal/scoring"
	_ "modernc.org/sqlite"
)

const CurrentSchemaVersion = 3

var ErrDBPathOverrideNotAllowed = errors.New("dbPath override requires allowTestDBPath=true")

func ResolveDBPath(pluginDir string, args map[string]any) (string, error) {
	executablePath := ""
	if exe, err := os.Executable(); err == nil {
		executablePath = exe
	}
	return resolveDBPath(pluginDir, args, executablePath)
}

func resolveDBPath(pluginDir string, args map[string]any, executablePath string) (string, error) {
	if raw, ok := stringArg(args, "dbPath"); ok && raw != "" {
		if !boolArg(args, "allowTestDBPath") && os.Getenv("STASH_HYBRID_ALLOW_DB_OVERRIDE") != "1" {
			return "", ErrDBPathOverrideNotAllowed
		}
		clean := filepath.Clean(raw)
		if !filepath.IsAbs(clean) {
			return "", fmt.Errorf("dbPath override must be absolute")
		}
		return clean, nil
	}
	resolvedPluginDir := strings.TrimSpace(pluginDir)
	if resolvedPluginDir == "" && executablePath != "" {
		resolvedPluginDir = filepath.Dir(executablePath)
	}
	if resolvedPluginDir == "" {
		return "", fmt.Errorf("PluginDir is required to resolve engine DB path")
	}
	return filepath.Join(resolvedPluginDir, "data", "recommendations.sqlite"), nil
}

func stringArg(args map[string]any, key string) (string, bool) {
	if args == nil {
		return "", false
	}
	v, ok := args[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func boolArg(args map[string]any, key string) bool {
	if args == nil {
		return false
	}
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	default:
		return false
	}
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	return &Store{db: db}, nil
}

func OpenReadOnly(path string) (*Store, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", filepath.ToSlash(path)))
	if err != nil {
		return nil, fmt.Errorf("open sqlite read-only: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Migrate() error {
	stmts := []string{
		`PRAGMA journal_mode = WAL;`,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS scenes (
			id TEXT PRIMARY KEY,
			title TEXT,
			file_name TEXT,
			details TEXT,
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
			stash_updated_at TEXT,
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_scenes_phash ON scenes(phash);`,
		`CREATE INDEX IF NOT EXISTS idx_scenes_updated_at ON scenes(updated_at);`,
		`CREATE TABLE IF NOT EXISTS recommendation_scores (
			model_version TEXT NOT NULL,
			source_scene_id TEXT NOT NULL,
			candidate_scene_id TEXT NOT NULL,
			rank INTEGER NOT NULL,
			score REAL NOT NULL,
			reasons_json TEXT NOT NULL DEFAULT '[]',
			breakdown_json TEXT NOT NULL DEFAULT '{}',
			weights_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			PRIMARY KEY (model_version, source_scene_id, candidate_scene_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_recommendation_scores_source_rank ON recommendation_scores(model_version, source_scene_id, rank);`,
		`CREATE TABLE IF NOT EXISTS engine_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			mode TEXT NOT NULL,
			status TEXT NOT NULL,
			started_at TEXT NOT NULL,
			finished_at TEXT,
			message TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS engine_status (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("run migration statement: %w", err)
		}
	}
	for _, column := range []struct{ name, ddl string }{
		{"file_name", `ALTER TABLE scenes ADD COLUMN file_name TEXT`},
		{"tags_json", `ALTER TABLE scenes ADD COLUMN tags_json TEXT NOT NULL DEFAULT '[]'`},
		{"rating100", `ALTER TABLE scenes ADD COLUMN rating100 INTEGER`},
		{"play_count", `ALTER TABLE scenes ADD COLUMN play_count INTEGER`},
		{"thumbnail_url", `ALTER TABLE scenes ADD COLUMN thumbnail_url TEXT`},
		{"stash_updated_at", `ALTER TABLE scenes ADD COLUMN stash_updated_at TEXT`},
	} {
		if err := s.ensureSceneColumn(column.name, column.ddl); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_scenes_stash_updated_at ON scenes(stash_updated_at);`); err != nil {
		return fmt.Errorf("create scenes.stash_updated_at index: %w", err)
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(?, ?)`, CurrentSchemaVersion, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("record schema migration: %w", err)
	}
	return nil
}

func (s *Store) ensureSceneColumn(name, ddl string) error {
	rows, err := s.db.Query(`PRAGMA table_info(scenes)`)
	if err != nil {
		return fmt.Errorf("inspect scenes schema: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var columnName, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &columnName, &typ, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan scenes schema: %w", err)
		}
		if columnName == name {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := s.db.Exec(ddl); err != nil {
		return fmt.Errorf("add scenes.%s: %w", name, err)
	}
	return nil
}

func (s *Store) SchemaVersion() (int, error) {
	var version sql.NullInt64
	err := s.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("query schema version: %w", err)
	}
	if !version.Valid {
		return 0, nil
	}
	return int(version.Int64), nil
}

func (s *Store) SceneCount() (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM scenes`).Scan(&count); err != nil {
		return 0, fmt.Errorf("query scene count: %w", err)
	}
	return count, nil
}

func (s *Store) RecommendationCount(modelVersion, sourceSceneID string) (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM recommendation_scores WHERE model_version = ? AND source_scene_id = ?`, modelVersion, sourceSceneID).Scan(&count); err != nil {
		return 0, fmt.Errorf("query recommendation count: %w", err)
	}
	return count, nil
}

type RecommendationCacheStatus struct {
	ModelVersion string `json:"modelVersion"`
	Sources      int    `json:"sources"`
	Rows         int    `json:"rows"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
}

type RecommendationSourceSample struct {
	ID                  string `json:"id"`
	Title               string `json:"title,omitempty"`
	FileName            string `json:"fileName,omitempty"`
	RecommendationCount int    `json:"recommendationCount"`
}

type SceneUpsertSummary struct {
	Seen       int      `json:"seen"`
	Inserted   int      `json:"inserted"`
	Updated    int      `json:"updated"`
	Skipped    int      `json:"skipped"`
	ChangedIDs []string `json:"changedIds"`
}

type PruneDeletedScenesSummary struct {
	CurrentSceneIDs            int      `json:"currentSceneIds"`
	LocalSceneIDs              int      `json:"localSceneIds"`
	DeletedScenes              int      `json:"deletedScenes"`
	DeletedSceneIDs            []string `json:"deletedSceneIds,omitempty"`
	DeletedRecommendationRows  int      `json:"deletedRecommendationRows"`
	ImpactedRecommendationRows int      `json:"impactedRecommendationRows"`
	ImpactedSourceIDs          []string `json:"impactedSourceIds,omitempty"`
	CacheBuilt                 bool     `json:"cacheBuilt"`
	CacheSources               int      `json:"cacheSources"`
	CacheRows                  int      `json:"cacheRows"`
	DryRun                     bool     `json:"dryRun,omitempty"`
}

func (s *Store) RecommendationCacheStatuses(ctx context.Context) ([]RecommendationCacheStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT model_version, count(DISTINCT source_scene_id), count(*), COALESCE(max(created_at), '') FROM recommendation_scores GROUP BY model_version ORDER BY model_version`)
	if err != nil {
		return nil, fmt.Errorf("query recommendation cache statuses: %w", err)
	}
	defer rows.Close()
	var out []RecommendationCacheStatus
	for rows.Next() {
		var status RecommendationCacheStatus
		if err := rows.Scan(&status.ModelVersion, &status.Sources, &status.Rows, &status.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan recommendation cache status: %w", err)
		}
		out = append(out, status)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) RecommendationSourceSamples(ctx context.Context, modelVersion string, limit int) ([]RecommendationSourceSample, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx, `SELECT
		s.id,
		COALESCE(s.title, ''),
		COALESCE(s.file_name, ''),
		count(r.candidate_scene_id) AS recommendation_count
		FROM recommendation_scores r
		JOIN scenes s ON s.id = r.source_scene_id
		WHERE r.model_version = ?
		GROUP BY s.id, s.title, s.file_name
		HAVING recommendation_count > 0
		ORDER BY CAST(s.id AS INTEGER), s.id
		LIMIT ?`, modelVersion, limit)
	if err != nil {
		return nil, fmt.Errorf("query recommendation source samples: %w", err)
	}
	defer rows.Close()
	var out []RecommendationSourceSample
	for rows.Next() {
		var sample RecommendationSourceSample
		if err := rows.Scan(&sample.ID, &sample.Title, &sample.FileName, &sample.RecommendationCount); err != nil {
			return nil, fmt.Errorf("scan recommendation source sample: %w", err)
		}
		out = append(out, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) UpsertScenes(ctx context.Context, scenes []scoring.Scene) error {
	_, err := s.UpsertScenesIncremental(ctx, scenes)
	return err
}

func (s *Store) UpsertScenesIncremental(ctx context.Context, scenes []scoring.Scene) (SceneUpsertSummary, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SceneUpsertSummary{}, fmt.Errorf("begin scene upsert: %w", err)
	}
	defer tx.Rollback()
	selectStmt, err := tx.PrepareContext(ctx, `SELECT COALESCE(stash_updated_at, '') FROM scenes WHERE id = ?`)
	if err != nil {
		return SceneUpsertSummary{}, fmt.Errorf("prepare scene revision lookup: %w", err)
	}
	defer selectStmt.Close()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO scenes(
		id, title, file_name, duration, width, height, tags_json, rating100, play_count,
		thumbnail_url, screenshot_url, sprite_image_url, sprite_vtt_url, phash, stash_updated_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		title=excluded.title,
		file_name=excluded.file_name,
		duration=excluded.duration,
		width=excluded.width,
		height=excluded.height,
		tags_json=excluded.tags_json,
		rating100=excluded.rating100,
		play_count=excluded.play_count,
		thumbnail_url=excluded.thumbnail_url,
		screenshot_url=excluded.screenshot_url,
		sprite_image_url=excluded.sprite_image_url,
		sprite_vtt_url=excluded.sprite_vtt_url,
		phash=excluded.phash,
		stash_updated_at=excluded.stash_updated_at,
		updated_at=excluded.updated_at`)
	if err != nil {
		return SceneUpsertSummary{}, fmt.Errorf("prepare scene upsert: %w", err)
	}
	defer stmt.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	summary := SceneUpsertSummary{}
	for _, scene := range scenes {
		if scene.ID == "" {
			continue
		}
		summary.Seen++
		stashUpdatedAt := strings.TrimSpace(scene.StashUpdatedAt)
		var existingStashUpdatedAt string
		existed := true
		err := selectStmt.QueryRowContext(ctx, scene.ID).Scan(&existingStashUpdatedAt)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				existed = false
			} else {
				return SceneUpsertSummary{}, fmt.Errorf("lookup scene revision %q: %w", scene.ID, err)
			}
		}
		if existed && stashUpdatedAt != "" && existingStashUpdatedAt == stashUpdatedAt {
			summary.Skipped++
			continue
		}
		tags, err := json.Marshal(scoring.NormalizeTags(scene.Tags))
		if err != nil {
			return SceneUpsertSummary{}, fmt.Errorf("encode scene tags: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, scene.ID, scene.Title, scene.FileName, nullFloat(scene.DurationSeconds), nullInt(scene.Width), nullInt(scene.Height), string(tags), nullInt(scene.Rating100), nullInt(scene.PlayCount), nullString(scene.ThumbnailURL), nullString(scene.ThumbnailURL), nullString(scene.SpriteImageURL), nullString(scene.SpriteVTTURL), nullString(scoring.NormalizePHash(scene.PHash)), nullString(stashUpdatedAt), now); err != nil {
			return SceneUpsertSummary{}, fmt.Errorf("upsert scene %q: %w", scene.ID, err)
		}
		if existed {
			summary.Updated++
		} else {
			summary.Inserted++
		}
		summary.ChangedIDs = append(summary.ChangedIDs, scene.ID)
	}
	if err := tx.Commit(); err != nil {
		return SceneUpsertSummary{}, err
	}
	return summary, nil
}

func (s *Store) PruneDeletedScenes(ctx context.Context, currentSceneIDs []string, modelVersion string, topN int, candidateLimit int, dryRun bool, progress BuildProgressFunc) (PruneDeletedScenesSummary, error) {
	currentSet := stringSet(currentSceneIDs)
	if len(currentSet) == 0 {
		return PruneDeletedScenesSummary{}, fmt.Errorf("current Stash scene ID set is empty; refusing to prune")
	}
	localScenes, err := s.ListScenes(ctx)
	if err != nil {
		return PruneDeletedScenesSummary{}, err
	}
	deletedIDs := make([]string, 0)
	for _, scene := range localScenes {
		if scene.ID == "" {
			continue
		}
		if _, ok := currentSet[scene.ID]; !ok {
			deletedIDs = append(deletedIDs, scene.ID)
		}
	}
	sort.Strings(deletedIDs)

	summary := PruneDeletedScenesSummary{
		CurrentSceneIDs: len(currentSet),
		LocalSceneIDs:   len(localScenes),
		DeletedScenes:   len(deletedIDs),
		DeletedSceneIDs: deletedIDs,
		DryRun:          dryRun,
	}
	if len(deletedIDs) == 0 {
		return summary, nil
	}

	deletedSet := stringSet(deletedIDs)
	cachedSources, err := s.sourcesWithCachedCandidates(ctx, deletedSet)
	if err != nil {
		return PruneDeletedScenesSummary{}, err
	}
	impactedSet := map[string]struct{}{}
	for _, id := range cachedSources {
		if _, deleted := deletedSet[id]; !deleted {
			impactedSet[id] = struct{}{}
		}
	}
	impactedIDs := make([]string, 0, len(impactedSet))
	for id := range impactedSet {
		impactedIDs = append(impactedIDs, id)
	}
	sort.Strings(impactedIDs)
	summary.ImpactedSourceIDs = impactedIDs

	if dryRun {
		return summary, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PruneDeletedScenesSummary{}, fmt.Errorf("begin deleted scene prune: %w", err)
	}
	defer tx.Rollback()
	if err := seedPruneDeletedSceneIDs(ctx, tx, deletedIDs); err != nil {
		return PruneDeletedScenesSummary{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM recommendation_scores WHERE source_scene_id IN (SELECT id FROM prune_deleted_scene_ids) OR candidate_scene_id IN (SELECT id FROM prune_deleted_scene_ids)`).Scan(&summary.DeletedRecommendationRows); err != nil {
		return PruneDeletedScenesSummary{}, fmt.Errorf("count pruned recommendation rows: %w", err)
	}
	if len(impactedIDs) > 0 {
		if err := seedPruneImpactedSourceIDs(ctx, tx, impactedIDs); err != nil {
			return PruneDeletedScenesSummary{}, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM recommendation_scores WHERE source_scene_id IN (SELECT id FROM prune_impacted_source_ids)`).Scan(&summary.ImpactedRecommendationRows); err != nil {
			return PruneDeletedScenesSummary{}, fmt.Errorf("count impacted recommendation rows: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM recommendation_scores WHERE source_scene_id IN (SELECT id FROM prune_deleted_scene_ids) OR candidate_scene_id IN (SELECT id FROM prune_deleted_scene_ids)`); err != nil {
		return PruneDeletedScenesSummary{}, fmt.Errorf("delete stale recommendation rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scenes WHERE id IN (SELECT id FROM prune_deleted_scene_ids)`); err != nil {
		return PruneDeletedScenesSummary{}, fmt.Errorf("delete stale scenes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PruneDeletedScenesSummary{}, fmt.Errorf("commit deleted scene prune: %w", err)
	}

	if modelVersion != "" && len(impactedIDs) > 0 {
		cacheSummary, err := s.BuildRecommendationCacheForSourcesWithProgress(ctx, modelVersion, impactedIDs, topN, candidateLimit, progress)
		if err != nil {
			return PruneDeletedScenesSummary{}, err
		}
		summary.CacheBuilt = true
		summary.CacheSources = cacheSummary.Sources
		summary.CacheRows = cacheSummary.Rows
	}
	return summary, nil
}

func seedPruneDeletedSceneIDs(ctx context.Context, tx *sql.Tx, ids []string) error {
	return seedTempIDs(ctx, tx, "prune_deleted_scene_ids", ids)
}

func seedPruneImpactedSourceIDs(ctx context.Context, tx *sql.Tx, ids []string) error {
	return seedTempIDs(ctx, tx, "prune_impacted_source_ids", ids)
}

func seedTempIDs(ctx context.Context, tx *sql.Tx, table string, ids []string) error {
	if table != "prune_deleted_scene_ids" && table != "prune_impacted_source_ids" {
		return fmt.Errorf("unsupported temp table %q", table)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS `+table+` (id TEXT PRIMARY KEY)`); err != nil {
		return fmt.Errorf("create temp prune id table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
		return fmt.Errorf("clear temp prune id table: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO `+table+`(id) VALUES(?)`)
	if err != nil {
		return fmt.Errorf("prepare temp prune id insert: %w", err)
	}
	defer stmt.Close()
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, id); err != nil {
			return fmt.Errorf("insert temp prune id %q: %w", id, err)
		}
	}
	return nil
}

func (s *Store) ListScenes(ctx context.Context) ([]scoring.Scene, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(title,''), COALESCE(file_name,''), COALESCE(duration,0), COALESCE(width,0), COALESCE(height,0), COALESCE(tags_json,'[]'), COALESCE(thumbnail_url, screenshot_url, ''), COALESCE(sprite_image_url,''), COALESCE(sprite_vtt_url,''), COALESCE(phash,''), COALESCE(rating100,0), COALESCE(play_count,0), COALESCE(stash_updated_at, '') FROM scenes ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query scenes: %w", err)
	}
	defer rows.Close()
	var scenes []scoring.Scene
	for rows.Next() {
		var scene scoring.Scene
		var tagsJSON string
		if err := rows.Scan(&scene.ID, &scene.Title, &scene.FileName, &scene.DurationSeconds, &scene.Width, &scene.Height, &tagsJSON, &scene.ThumbnailURL, &scene.SpriteImageURL, &scene.SpriteVTTURL, &scene.PHash, &scene.Rating100, &scene.PlayCount, &scene.StashUpdatedAt); err != nil {
			return nil, fmt.Errorf("scan scene: %w", err)
		}
		_ = json.Unmarshal([]byte(tagsJSON), &scene.Tags)
		scene.PHash = scoring.NormalizePHash(scene.PHash)
		scenes = append(scenes, scene)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return scenes, nil
}

type BuildCacheSummary struct {
	Sources int
	Rows    int
	Partial bool
}

type BuildProgressFunc func(processed int, total int)

func (s *Store) BuildRecommendationCache(ctx context.Context, modelVersion string, topN int, candidateLimit int) (BuildCacheSummary, error) {
	return s.BuildRecommendationCacheWithProgress(ctx, modelVersion, topN, candidateLimit, nil)
}

func (s *Store) BuildRecommendationCacheWithProgress(ctx context.Context, modelVersion string, topN int, candidateLimit int, progress BuildProgressFunc) (BuildCacheSummary, error) {
	if topN <= 0 {
		topN = 50
	}
	if candidateLimit <= 0 {
		candidateLimit = 1000
	}
	scenes, err := s.ListScenes(ctx)
	if err != nil {
		return BuildCacheSummary{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BuildCacheSummary{}, fmt.Errorf("begin cache build: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM recommendation_scores WHERE model_version = ?`, modelVersion); err != nil {
		return BuildCacheSummary{}, fmt.Errorf("clear existing cache: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO recommendation_scores(model_version, source_scene_id, candidate_scene_id, rank, score, reasons_json, breakdown_json, weights_json, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return BuildCacheSummary{}, fmt.Errorf("prepare cache insert: %w", err)
	}
	defer stmt.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	summary := BuildCacheSummary{Sources: len(scenes)}
	for sourceIndex, source := range scenes {
		candidates := scoreCandidates(source, scenes, candidateLimit)
		if len(candidates) > topN {
			candidates = candidates[:topN]
		}
		for idx, result := range candidates {
			reasons, _ := json.Marshal(result.Reasons)
			breakdown, _ := json.Marshal(result.Breakdown)
			weights, _ := json.Marshal(result.Weights)
			if _, err := stmt.ExecContext(ctx, modelVersion, source.ID, result.SceneID, idx+1, result.Score, string(reasons), string(breakdown), string(weights), now); err != nil {
				return BuildCacheSummary{}, fmt.Errorf("insert cache row: %w", err)
			}
			summary.Rows++
		}
		if progress != nil {
			progress(sourceIndex+1, len(scenes))
		}
	}
	if err := tx.Commit(); err != nil {
		return BuildCacheSummary{}, fmt.Errorf("commit cache build: %w", err)
	}
	return summary, nil
}

func (s *Store) BuildRecommendationCacheForSourcesWithProgress(ctx context.Context, modelVersion string, sourceIDs []string, topN int, candidateLimit int, progress BuildProgressFunc) (BuildCacheSummary, error) {
	if topN <= 0 {
		topN = 50
	}
	if candidateLimit <= 0 {
		candidateLimit = 1000
	}
	sourceSet := stringSet(sourceIDs)
	if len(sourceSet) == 0 {
		return BuildCacheSummary{Partial: true}, nil
	}
	scenes, err := s.ListScenes(ctx)
	if err != nil {
		return BuildCacheSummary{}, err
	}
	selected := make([]scoring.Scene, 0, len(sourceSet))
	for _, scene := range scenes {
		if _, ok := sourceSet[scene.ID]; ok {
			selected = append(selected, scene)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BuildCacheSummary{}, fmt.Errorf("begin partial cache build: %w", err)
	}
	defer tx.Rollback()
	if err := deleteCacheSources(ctx, tx, modelVersion, sourceSet); err != nil {
		return BuildCacheSummary{}, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO recommendation_scores(model_version, source_scene_id, candidate_scene_id, rank, score, reasons_json, breakdown_json, weights_json, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return BuildCacheSummary{}, fmt.Errorf("prepare partial cache insert: %w", err)
	}
	defer stmt.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	summary := BuildCacheSummary{Sources: len(selected), Partial: true}
	for sourceIndex, source := range selected {
		candidates := scoreCandidates(source, scenes, candidateLimit)
		if len(candidates) > topN {
			candidates = candidates[:topN]
		}
		for idx, result := range candidates {
			reasons, _ := json.Marshal(result.Reasons)
			breakdown, _ := json.Marshal(result.Breakdown)
			weights, _ := json.Marshal(result.Weights)
			if _, err := stmt.ExecContext(ctx, modelVersion, source.ID, result.SceneID, idx+1, result.Score, string(reasons), string(breakdown), string(weights), now); err != nil {
				return BuildCacheSummary{}, fmt.Errorf("insert partial cache row: %w", err)
			}
			summary.Rows++
		}
		if progress != nil {
			progress(sourceIndex+1, len(selected))
		}
	}
	if err := tx.Commit(); err != nil {
		return BuildCacheSummary{}, fmt.Errorf("commit partial cache build: %w", err)
	}
	return summary, nil
}

func (s *Store) RecommendationCacheRowCount(ctx context.Context, modelVersion string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM recommendation_scores WHERE model_version = ?`, modelVersion).Scan(&count); err != nil {
		return 0, fmt.Errorf("query recommendation cache row count: %w", err)
	}
	return count, nil
}

func (s *Store) ImpactedRecommendationSourceIDs(ctx context.Context, changedIDs []string) ([]string, error) {
	changedSet := stringSet(changedIDs)
	if len(changedSet) == 0 {
		return nil, nil
	}
	scenes, err := s.ListScenes(ctx)
	if err != nil {
		return nil, err
	}
	changedScenes := make([]scoring.Scene, 0, len(changedSet))
	for _, scene := range scenes {
		if _, ok := changedSet[scene.ID]; ok {
			changedScenes = append(changedScenes, scene)
		}
	}
	sourceSet := map[string]struct{}{}
	for _, changed := range changedScenes {
		sourceSet[changed.ID] = struct{}{}
	}
	for _, source := range scenes {
		for _, changed := range changedScenes {
			if scoring.HasLiteCandidateSignal(source, changed) {
				sourceSet[source.ID] = struct{}{}
				break
			}
		}
	}
	cachedSources, err := s.sourcesWithCachedCandidates(ctx, changedSet)
	if err != nil {
		return nil, err
	}
	for _, id := range cachedSources {
		sourceSet[id] = struct{}{}
	}
	out := make([]string, 0, len(sourceSet))
	for id := range sourceSet {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) sourcesWithCachedCandidates(ctx context.Context, candidateSet map[string]struct{}) ([]string, error) {
	if len(candidateSet) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(candidateSet))
	args := make([]any, 0, len(candidateSet))
	for id := range candidateSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	placeholders := make([]string, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	query := `SELECT DISTINCT source_scene_id FROM recommendation_scores WHERE candidate_scene_id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query cache sources for changed candidates: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan cache source for changed candidate: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scoreCandidates(source scoring.Scene, scenes []scoring.Scene, candidateLimit int) []scoring.Result {
	results := make([]scoring.Result, 0, min(candidateLimit, len(scenes)))
	for _, candidate := range scenes {
		if !scoring.HasLiteCandidateSignal(source, candidate) {
			continue
		}
		result, ok := scoring.HybridV3LiteScore(source, candidate)
		if !ok || result.Score <= 0 {
			continue
		}
		results = append(results, result)
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].SceneID < results[j].SceneID
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > candidateLimit {
		return results[:candidateLimit]
	}
	return results
}

func stringSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func deleteCacheSources(ctx context.Context, tx *sql.Tx, modelVersion string, sourceSet map[string]struct{}) error {
	if len(sourceSet) == 0 {
		return nil
	}
	ids := make([]string, 0, len(sourceSet))
	args := make([]any, 0, len(sourceSet)+1)
	args = append(args, modelVersion)
	for id := range sourceSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	placeholders := make([]string, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	query := `DELETE FROM recommendation_scores WHERE model_version = ? AND source_scene_id IN (` + strings.Join(placeholders, ",") + `)`
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("clear partial cache: %w", err)
	}
	return nil
}

type PublicScene struct {
	ID              string   `json:"id"`
	Title           string   `json:"title,omitempty"`
	FileName        string   `json:"fileName,omitempty"`
	DurationSeconds float64  `json:"durationSeconds,omitempty"`
	Width           int      `json:"width,omitempty"`
	Height          int      `json:"height,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	ThumbnailURL    string   `json:"thumbnailUrl,omitempty"`
	SpriteImageURL  string   `json:"spriteImageUrl,omitempty"`
	SpriteVTTURL    string   `json:"spriteVttUrl,omitempty"`
}

type Recommendation struct {
	SceneID   string            `json:"sceneId"`
	Score     float64           `json:"score"`
	Reasons   []string          `json:"reasons"`
	Breakdown scoring.Breakdown `json:"breakdown"`
	Weights   scoring.Weights   `json:"weights"`
	Scene     PublicScene       `json:"scene"`
}

func (s *Store) Recommendations(ctx context.Context, modelVersion, sourceSceneID string, limit int) ([]Recommendation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		r.candidate_scene_id, r.score, r.reasons_json, r.breakdown_json, r.weights_json,
		s.id, COALESCE(s.title,''), COALESCE(s.file_name,''), COALESCE(s.duration,0), COALESCE(s.width,0), COALESCE(s.height,0), COALESCE(s.tags_json,'[]'), COALESCE(s.thumbnail_url, s.screenshot_url, ''), COALESCE(s.sprite_image_url,''), COALESCE(s.sprite_vtt_url,'')
		FROM recommendation_scores r
		JOIN scenes s ON s.id = r.candidate_scene_id
		WHERE r.model_version = ? AND r.source_scene_id = ?
		ORDER BY r.rank ASC
		LIMIT ?`, modelVersion, sourceSceneID, limit)
	if err != nil {
		return nil, fmt.Errorf("query recommendations: %w", err)
	}
	defer rows.Close()
	var out []Recommendation
	for rows.Next() {
		var rec Recommendation
		var reasonsJSON, breakdownJSON, weightsJSON, tagsJSON string
		if err := rows.Scan(&rec.SceneID, &rec.Score, &reasonsJSON, &breakdownJSON, &weightsJSON, &rec.Scene.ID, &rec.Scene.Title, &rec.Scene.FileName, &rec.Scene.DurationSeconds, &rec.Scene.Width, &rec.Scene.Height, &tagsJSON, &rec.Scene.ThumbnailURL, &rec.Scene.SpriteImageURL, &rec.Scene.SpriteVTTURL); err != nil {
			return nil, fmt.Errorf("scan recommendation: %w", err)
		}
		_ = json.Unmarshal([]byte(reasonsJSON), &rec.Reasons)
		_ = json.Unmarshal([]byte(breakdownJSON), &rec.Breakdown)
		_ = json.Unmarshal([]byte(weightsJSON), &rec.Weights)
		_ = json.Unmarshal([]byte(tagsJSON), &rec.Scene.Tags)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullFloat(value float64) any {
	if value == 0 {
		return nil
	}
	return value
}
