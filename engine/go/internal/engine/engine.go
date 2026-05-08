package engine

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	stashgraphql "github.com/gomeng-dev/stash-web-sprite-similarity-lab/engine/go/internal/graphql"
	"github.com/gomeng-dev/stash-web-sprite-similarity-lab/engine/go/internal/pluginio"
	"github.com/gomeng-dev/stash-web-sprite-similarity-lab/engine/go/internal/scoring"
	"github.com/gomeng-dev/stash-web-sprite-similarity-lab/engine/go/internal/store"
)

const EngineVersion = "0.3.1"
const DefaultModelVersion = scoring.ModelVersionHybridV3Lite

type Payload map[string]any

func Run(input pluginio.Input) Payload {
	mode := strings.ToLower(strings.TrimSpace(stringArg(input.Args, "mode", "status")))
	if mode == "" {
		mode = "status"
	}

	switch mode {
	case "status":
		return runStatus(input)
	case "bootstrap":
		return runBootstrap(input)
	case "index-scenes":
		return runIndexScenes(input)
	case "build-cache":
		return runBuildCache(input)
	case "prune-deleted-scenes":
		return runPruneDeletedScenes(input)
	case "import-db":
		return runImportDB(input)
	case "dev-test-100":
		return runDevTest100(input)
	case "recommend":
		return runRecommend(input)
	default:
		return errorPayload(mode, "unsupported-mode", fmt.Sprintf("unsupported mode %q", mode))
	}
}

func basePayload(mode string) Payload {
	return Payload{
		"ok":            true,
		"mode":          mode,
		"engine":        "stash-hybrid-go",
		"engineVersion": EngineVersion,
	}
}

func emitProgress(progress float64) {
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	// Stash raw plugins report task progress by writing a progress-level log line
	// to stderr: SOH + 'p' + STX + float. See Stash pkg/plugin/common/log.
	fmt.Fprintf(os.Stderr, "\x01p\x02%.6f\n", progress)
}

func runStatus(input pluginio.Input) Payload {
	payload := basePayload("status")
	payload["capabilities"] = []string{"status", "bootstrap", "index-scenes", "build-cache", "prune-deleted-scenes", "import-db", "dev-test-100", "recommend"}
	payload["defaultModelVersion"] = DefaultModelVersion

	dbPath, err := store.ResolveDBPath(input.PluginDir, input.Args)
	if err != nil {
		payload["database"] = map[string]any{"configured": false, "exists": false, "schemaVersion": 0}
		payload["fallbackReason"] = "db-path-unavailable"
		return payload
	}
	info, statErr := os.Stat(dbPath)
	if statErr != nil {
		payload["database"] = map[string]any{"configured": true, "exists": false, "schemaVersion": 0}
		return payload
	}
	if info.IsDir() {
		payload["database"] = map[string]any{"configured": true, "exists": false, "schemaVersion": 0}
		payload["fallbackReason"] = "db-path-is-directory"
		return payload
	}

	s, err := store.OpenReadOnly(dbPath)
	if err != nil {
		payload["database"] = map[string]any{"configured": true, "exists": true, "schemaVersion": 0}
		payload["fallbackReason"] = "db-open-failed"
		return payload
	}
	defer s.Close()
	version, err := s.SchemaVersion()
	if err != nil {
		payload["database"] = map[string]any{"configured": true, "exists": true, "schemaVersion": 0}
		payload["fallbackReason"] = "schema-version-unavailable"
		return payload
	}
	payload["database"] = map[string]any{
		"configured":    true,
		"exists":        true,
		"schemaVersion": version,
		"sizeBytes":     info.Size(),
		"updatedAt":     info.ModTime().UTC().Format(time.RFC3339),
	}
	if sceneCount, err := s.SceneCount(); err == nil {
		payload["sceneCount"] = sceneCount
	}
	if cacheStatuses, err := s.RecommendationCacheStatuses(context.Background()); err == nil {
		payload["recommendationCaches"] = cacheStatuses
	}
	return payload
}

func runBootstrap(input pluginio.Input) Payload {
	emitProgress(0.01)
	payload := basePayload("bootstrap")
	dbPath, err := store.ResolveDBPath(input.PluginDir, input.Args)
	if err != nil {
		return errorPayload("bootstrap", "db-path-unavailable", err.Error())
	}
	if err := os.MkdirAll(parentDir(dbPath), 0o755); err != nil {
		return errorPayload("bootstrap", "mkdir-data-dir-failed", err.Error())
	}
	s, err := store.Open(dbPath)
	if err != nil {
		return errorPayload("bootstrap", "db-open-failed", err.Error())
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return errorPayload("bootstrap", "migration-failed", err.Error())
	}
	emitProgress(0.08)
	version, err := s.SchemaVersion()
	if err != nil {
		return errorPayload("bootstrap", "schema-version-unavailable", err.Error())
	}
	payload["bootstrapped"] = true
	payload["database"] = map[string]any{"configured": true, "exists": true, "schemaVersion": version}

	if _, ok := graphQLConnection(input); ok {
		merge(payload, indexAndBuild(context.Background(), input, s, "bootstrap"))
	} else {
		payload["indexedScenes"] = 0
		payload["cacheBuilt"] = false
		payload["fallbackReason"] = "stash-connection-unavailable"
		payload["message"] = "schema migrated; provide stashGraphqlUrl/stashBaseUrl or Stash server connection to index scenes and build the lite cache"
	}
	emitProgress(1)
	return payload
}

func runIndexScenes(input pluginio.Input) Payload {
	emitProgress(0.01)
	payload := basePayload("index-scenes")
	dbPath, err := store.ResolveDBPath(input.PluginDir, input.Args)
	if err != nil {
		return errorPayload("index-scenes", "db-path-unavailable", err.Error())
	}
	if err := os.MkdirAll(parentDir(dbPath), 0o755); err != nil {
		return errorPayload("index-scenes", "mkdir-data-dir-failed", err.Error())
	}
	s, err := store.Open(dbPath)
	if err != nil {
		return errorPayload("index-scenes", "db-open-failed", err.Error())
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return errorPayload("index-scenes", "migration-failed", err.Error())
	}
	emitProgress(0.08)
	if _, ok := graphQLConnection(input); !ok {
		payload["ok"] = false
		payload["indexedScenes"] = 0
		payload["cacheBuilt"] = false
		payload["fallbackReason"] = "stash-connection-unavailable"
		payload["error"] = "provide stashGraphqlUrl/stashBaseUrl or a Stash server connection"
		return payload
	}
	indexPayload, _ := indexScenesFromStash(context.Background(), input, s, "index-scenes")
	merge(payload, indexPayload)
	emitProgress(1)
	return payload
}

func runBuildCache(input pluginio.Input) Payload {
	emitProgress(0.01)
	payload := basePayload("build-cache")
	dbPath, err := store.ResolveDBPath(input.PluginDir, input.Args)
	if err != nil {
		return errorPayload("build-cache", "db-path-unavailable", err.Error())
	}
	if err := os.MkdirAll(parentDir(dbPath), 0o755); err != nil {
		return errorPayload("build-cache", "mkdir-data-dir-failed", err.Error())
	}
	s, err := store.Open(dbPath)
	if err != nil {
		return errorPayload("build-cache", "db-open-failed", err.Error())
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return errorPayload("build-cache", "migration-failed", err.Error())
	}
	emitProgress(0.08)
	merge(payload, buildCacheFromIndexedScenes(context.Background(), input, s, "build-cache"))
	emitProgress(1)
	return payload
}

func runPruneDeletedScenes(input pluginio.Input) Payload {
	emitProgress(0.01)
	payload := basePayload("prune-deleted-scenes")
	dbPath, err := store.ResolveDBPath(input.PluginDir, input.Args)
	if err != nil {
		return errorPayload("prune-deleted-scenes", "db-path-unavailable", err.Error())
	}
	dryRun := boolArg(input.Args, "dryRun", false)
	if _, ok := input.Args["maxScenes"]; ok {
		return errorPayload("prune-deleted-scenes", "bounded-prune-refused", "prune-deleted-scenes must compare against the full Stash scene ID set; maxScenes is not allowed")
	}
	if _, ok := input.Args["limitScenes"]; ok {
		return errorPayload("prune-deleted-scenes", "bounded-prune-refused", "prune-deleted-scenes must compare against the full Stash scene ID set; limitScenes is not allowed")
	}
	var s *store.Store
	if dryRun {
		s, err = store.OpenReadOnly(dbPath)
	} else {
		if err := os.MkdirAll(parentDir(dbPath), 0o755); err != nil {
			return errorPayload("prune-deleted-scenes", "mkdir-data-dir-failed", err.Error())
		}
		s, err = store.Open(dbPath)
	}
	if err != nil {
		return errorPayload("prune-deleted-scenes", "db-open-failed", err.Error())
	}
	defer s.Close()
	if !dryRun {
		if err := s.Migrate(); err != nil {
			return errorPayload("prune-deleted-scenes", "migration-failed", err.Error())
		}
	}
	connection, ok := graphQLConnection(input)
	if !ok {
		payload["ok"] = false
		payload["fallbackReason"] = "stash-connection-unavailable"
		payload["error"] = "provide stashGraphqlUrl/stashBaseUrl or a Stash server connection"
		payload["cacheBuilt"] = false
		return payload
	}
	emitProgress(0.12)
	client := stashgraphql.Client{URL: connection.url, APIKey: connection.apiKey, CookieName: connection.cookieName, CookieValue: connection.cookieValue}
	maxScenes := intArg(input.Args, "maxScenes", intArg(input.Args, "limitScenes", 0))
	perPage := intArg(input.Args, "perPage", 1000)
	sceneIDs, err := client.FetchAllSceneIDs(context.Background(), maxScenes, perPage)
	if err != nil {
		payload["ok"] = false
		payload["fallbackReason"] = "stash-graphql-fetch-failed"
		payload["error"] = err.Error()
		payload["cacheBuilt"] = false
		return payload
	}
	if len(sceneIDs) == 0 {
		payload["ok"] = false
		payload["fallbackReason"] = "empty-stash-scene-id-set"
		payload["error"] = "Stash returned zero scene IDs; refusing to prune"
		payload["stashSceneCount"] = 0
		payload["cacheBuilt"] = false
		return payload
	}
	emitProgress(0.35)
	modelVersion := stringArg(input.Args, "modelVersion", DefaultModelVersion)
	topN := intArg(input.Args, "topN", clampLimit(input.Args["limit"], 50))
	candidateLimit := intArg(input.Args, "candidateLimit", 1000)
	summary, err := s.PruneDeletedScenes(context.Background(), sceneIDs, modelVersion, topN, candidateLimit, dryRun, func(processed int, total int) {
		if total <= 0 {
			return
		}
		if processed == total || processed%100 == 0 {
			emitProgress(0.35 + (0.60 * float64(processed) / float64(total)))
		}
	})
	if err != nil {
		payload["ok"] = false
		payload["fallbackReason"] = "deleted-scene-prune-failed"
		payload["error"] = err.Error()
		payload["cacheBuilt"] = false
		return payload
	}
	payload["stashSceneCount"] = len(sceneIDs)
	payload["localSceneCount"] = summary.LocalSceneIDs
	payload["deletedScenes"] = summary.DeletedScenes
	payload["deletedRecommendationRows"] = summary.DeletedRecommendationRows
	payload["impactedRecommendationRows"] = summary.ImpactedRecommendationRows
	payload["impactedSources"] = len(summary.ImpactedSourceIDs)
	payload["cacheBuilt"] = summary.CacheBuilt
	payload["cacheScope"] = "none"
	if summary.CacheBuilt {
		payload["cacheScope"] = "partial"
	}
	payload["modelVersion"] = modelVersion
	payload["cacheRows"] = summary.CacheRows
	payload["cacheSources"] = summary.CacheSources
	if dryRun {
		payload["dryRun"] = true
	}
	if len(summary.DeletedSceneIDs) > 0 {
		limit := min(len(summary.DeletedSceneIDs), 25)
		payload["deletedSceneIds"] = summary.DeletedSceneIDs[:limit]
		payload["deletedSceneIdsTruncated"] = len(summary.DeletedSceneIDs) > limit
	}
	emitProgress(1)
	return payload
}

func runImportDB(input pluginio.Input) Payload {
	emitProgress(0.01)
	payload := basePayload("import-db")
	sourcePath := strings.TrimSpace(stringArg(input.Args, "sourceDbPath", ""))
	if sourcePath == "" {
		return errorPayload("import-db", "source-db-path-required", "sourceDbPath is required")
	}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return errorPayload("import-db", "source-db-unavailable", err.Error())
	}
	if sourceInfo.IsDir() {
		return errorPayload("import-db", "source-db-is-directory", sourcePath)
	}
	dbPath, err := store.ResolveDBPath(input.PluginDir, input.Args)
	if err != nil {
		return errorPayload("import-db", "db-path-unavailable", err.Error())
	}
	targetDir := parentDir(dbPath)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return errorPayload("import-db", "mkdir-data-dir-failed", err.Error())
	}
	sourceAbs, _ := filepath.Abs(sourcePath)
	targetAbs, _ := filepath.Abs(dbPath)
	if sourceAbs == targetAbs {
		return errorPayload("import-db", "source-db-is-current-db", "sourceDbPath already points at the active engine DB")
	}
	emitProgress(0.12)
	tmpPath := filepath.Join(targetDir, fmt.Sprintf(".import-%d-recommendations.sqlite", time.Now().UnixNano()))
	if err := copySQLiteBundle(sourcePath, tmpPath); err != nil {
		cleanupSQLiteBundle(tmpPath)
		return errorPayload("import-db", "source-db-copy-failed", err.Error())
	}
	emitProgress(0.35)
	imported, err := store.Open(tmpPath)
	if err != nil {
		cleanupSQLiteBundle(tmpPath)
		return errorPayload("import-db", "import-db-open-failed", err.Error())
	}
	if err := imported.Migrate(); err != nil {
		imported.Close()
		cleanupSQLiteBundle(tmpPath)
		return errorPayload("import-db", "import-db-migration-failed", err.Error())
	}
	version, versionErr := imported.SchemaVersion()
	sceneCount, sceneErr := imported.SceneCount()
	cacheStatuses, cacheErr := imported.RecommendationCacheStatuses(context.Background())
	if err := imported.Close(); err != nil {
		cleanupSQLiteBundle(tmpPath)
		return errorPayload("import-db", "import-db-close-failed", err.Error())
	}
	if versionErr != nil {
		cleanupSQLiteBundle(tmpPath)
		return errorPayload("import-db", "import-db-schema-version-unavailable", versionErr.Error())
	}
	if sceneErr != nil {
		cleanupSQLiteBundle(tmpPath)
		return errorPayload("import-db", "import-db-scene-count-unavailable", sceneErr.Error())
	}
	emitProgress(0.65)
	backupPath, err := moveExistingDatabaseAside(dbPath, "pre-import")
	if err != nil {
		cleanupSQLiteBundle(tmpPath)
		return errorPayload("import-db", "current-db-backup-failed", err.Error())
	}
	if err := renameSQLiteBundle(tmpPath, dbPath); err != nil {
		cleanupSQLiteBundle(tmpPath)
		return errorPayload("import-db", "import-db-activate-failed", err.Error())
	}
	emitProgress(0.95)
	payload["imported"] = true
	payload["sourceDbPath"] = sourcePath
	payload["importedSizeBytes"] = sourceInfo.Size()
	payload["previousDatabaseBackup"] = backupPath
	payload["sceneCount"] = sceneCount
	payload["database"] = map[string]any{"configured": true, "exists": true, "schemaVersion": version}
	if cacheErr == nil {
		payload["recommendationCaches"] = cacheStatuses
	}
	emitProgress(1)
	return payload
}

func runDevTest100(input pluginio.Input) Payload {
	const devSceneLimit = 100
	emitProgress(0.01)
	payload := basePayload("dev-test-100")
	payload["developmentOnly"] = true
	payload["maxScenes"] = devSceneLimit
	dbPath, err := store.ResolveDBPath(input.PluginDir, input.Args)
	if err != nil {
		return errorPayload("dev-test-100", "db-path-unavailable", err.Error())
	}
	if err := os.MkdirAll(parentDir(dbPath), 0o755); err != nil {
		return errorPayload("dev-test-100", "mkdir-data-dir-failed", err.Error())
	}
	backupPath, err := moveExistingDatabaseAside(dbPath, "dev-test100")
	if err != nil {
		return errorPayload("dev-test-100", "previous-db-backup-failed", err.Error())
	}
	if backupPath != "" {
		payload["previousDatabaseBackup"] = backupPath
	}
	s, err := store.Open(dbPath)
	if err != nil {
		return errorPayload("dev-test-100", "db-open-failed", err.Error())
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return errorPayload("dev-test-100", "migration-failed", err.Error())
	}
	emitProgress(0.08)
	if _, ok := graphQLConnection(input); !ok {
		payload["ok"] = false
		payload["indexedScenes"] = 0
		payload["cacheBuilt"] = false
		payload["fallbackReason"] = "stash-connection-unavailable"
		payload["error"] = "provide stashGraphqlUrl/stashBaseUrl or a Stash server connection"
		return payload
	}
	devArgs := cloneArgs(input.Args)
	devArgs["maxScenes"] = devSceneLimit
	devArgs["limitScenes"] = devSceneLimit
	if _, ok := devArgs["topN"]; !ok {
		devArgs["topN"] = 50
	}
	devArgs["modelVersion"] = DefaultModelVersion
	devInput := input
	devInput.Args = devArgs
	merge(payload, indexAndBuild(context.Background(), devInput, s, "dev-test-100"))
	if payload["ok"] == false {
		return payload
	}
	samples, sampleErr := s.RecommendationSourceSamples(context.Background(), DefaultModelVersion, 5)
	if sampleErr == nil {
		payload["verificationScenes"] = samples
	} else {
		payload["verificationSceneError"] = sampleErr.Error()
	}
	fmt.Fprintf(os.Stderr, "[StashHybridRecommendations] Dev 100-scene test DB built: scenes=%v cacheRows=%v model=%s db=%s\n", payload["indexedScenes"], payload["cacheRows"], DefaultModelVersion, dbPath)
	if len(samples) > 0 {
		fmt.Fprintln(os.Stderr, "[StashHybridRecommendations] Verification scenes (open these in the recommendation tab):")
		for _, sample := range samples {
			title := sample.Title
			if title == "" {
				title = sample.FileName
			}
			fmt.Fprintf(os.Stderr, "[StashHybridRecommendations] - /scenes/%s · %s · recommendations=%d\n", sample.ID, title, sample.RecommendationCount)
		}
	}
	emitProgress(1)
	return payload
}

func indexAndBuild(ctx context.Context, input pluginio.Input, s *store.Store, mode string) Payload {
	payload, changedIDs := indexScenesFromStash(ctx, input, s, mode)
	if payload["ok"] == false {
		return payload
	}
	cachePayload := buildCacheForIndexedChanges(ctx, input, s, mode, changedIDs)
	merge(payload, cachePayload)
	return payload
}

func indexScenesFromStash(ctx context.Context, input pluginio.Input, s *store.Store, mode string) (Payload, []string) {
	payload := Payload{}
	connection, ok := graphQLConnection(input)
	if !ok {
		payload["ok"] = false
		payload["fallbackReason"] = "stash-connection-unavailable"
		payload["error"] = "provide stashGraphqlUrl/stashBaseUrl or a Stash server connection"
		return payload, nil
	}
	emitProgress(0.12)
	client := stashgraphql.Client{URL: connection.url, APIKey: connection.apiKey, CookieName: connection.cookieName, CookieValue: connection.cookieValue}
	maxScenes := intArg(input.Args, "maxScenes", intArg(input.Args, "limitScenes", 0))
	perPage := intArg(input.Args, "perPage", 500)
	scenes, err := client.FetchAllScenes(ctx, maxScenes, perPage)
	if err != nil {
		payload["ok"] = false
		payload["indexedScenes"] = 0
		payload["cacheBuilt"] = false
		payload["fallbackReason"] = "stash-graphql-fetch-failed"
		payload["error"] = err.Error()
		return payload, nil
	}
	emitProgress(0.30)
	upsertSummary, err := s.UpsertScenesIncremental(ctx, scenes)
	if err != nil {
		payload["ok"] = false
		payload["indexedScenes"] = 0
		payload["cacheBuilt"] = false
		payload["fallbackReason"] = "scene-upsert-failed"
		payload["error"] = err.Error()
		return payload, nil
	}
	emitProgress(0.95)
	payload["mode"] = mode
	payload["indexedScenes"] = len(scenes)
	payload["insertedScenes"] = upsertSummary.Inserted
	payload["updatedScenes"] = upsertSummary.Updated
	payload["skippedScenes"] = upsertSummary.Skipped
	payload["changedScenes"] = len(upsertSummary.ChangedIDs)
	payload["cacheBuilt"] = false
	return payload, upsertSummary.ChangedIDs
}

func buildCacheFromIndexedScenes(ctx context.Context, input pluginio.Input, s *store.Store, mode string) Payload {
	payload := Payload{}
	emitProgress(0.12)
	modelVersion := stringArg(input.Args, "modelVersion", DefaultModelVersion)
	topN := intArg(input.Args, "topN", clampLimit(input.Args["limit"], 50))
	candidateLimit := intArg(input.Args, "candidateLimit", 1000)
	summary, err := s.BuildRecommendationCacheWithProgress(ctx, modelVersion, topN, candidateLimit, func(processed int, total int) {
		if total <= 0 {
			return
		}
		if processed == total || processed%100 == 0 {
			emitProgress(0.12 + (0.83 * float64(processed) / float64(total)))
		}
	})
	if err != nil {
		payload["ok"] = false
		payload["cacheBuilt"] = false
		payload["fallbackReason"] = "cache-build-failed"
		payload["error"] = err.Error()
		return payload
	}
	payload["mode"] = mode
	payload["cacheBuilt"] = true
	payload["modelVersion"] = modelVersion
	payload["cacheRows"] = summary.Rows
	payload["cacheSources"] = summary.Sources
	emitProgress(0.98)
	return payload
}

func buildCacheForIndexedChanges(ctx context.Context, input pluginio.Input, s *store.Store, mode string, changedIDs []string) Payload {
	modelVersion := stringArg(input.Args, "modelVersion", DefaultModelVersion)
	fullCache := boolArg(input.Args, "fullCache", false)
	cacheRows, err := s.RecommendationCacheRowCount(ctx, modelVersion)
	if err != nil {
		return Payload{"ok": false, "cacheBuilt": false, "fallbackReason": "cache-status-failed", "error": err.Error()}
	}
	if fullCache || cacheRows == 0 {
		payload := buildCacheFromIndexedScenes(ctx, input, s, mode)
		payload["cacheScope"] = "full"
		return payload
	}
	if len(changedIDs) == 0 {
		return Payload{
			"mode":         mode,
			"cacheBuilt":   false,
			"cacheSkipped": true,
			"cacheScope":   "none",
			"modelVersion": modelVersion,
			"cacheRows":    0,
			"cacheSources": 0,
		}
	}
	impactedIDs, err := s.ImpactedRecommendationSourceIDs(ctx, changedIDs)
	if err != nil {
		return Payload{"ok": false, "cacheBuilt": false, "fallbackReason": "cache-impact-analysis-failed", "error": err.Error()}
	}
	if len(impactedIDs) == 0 {
		return Payload{
			"mode":            mode,
			"cacheBuilt":      false,
			"cacheSkipped":    true,
			"cacheScope":      "none",
			"modelVersion":    modelVersion,
			"cacheRows":       0,
			"cacheSources":    0,
			"impactedSources": 0,
		}
	}
	topN := intArg(input.Args, "topN", clampLimit(input.Args["limit"], 50))
	candidateLimit := intArg(input.Args, "candidateLimit", 1000)
	emitProgress(0.12)
	summary, err := s.BuildRecommendationCacheForSourcesWithProgress(ctx, modelVersion, impactedIDs, topN, candidateLimit, func(processed int, total int) {
		if total <= 0 {
			return
		}
		if processed == total || processed%100 == 0 {
			emitProgress(0.12 + (0.83 * float64(processed) / float64(total)))
		}
	})
	if err != nil {
		return Payload{"ok": false, "cacheBuilt": false, "fallbackReason": "partial-cache-build-failed", "error": err.Error()}
	}
	payload := Payload{
		"mode":            mode,
		"cacheBuilt":      true,
		"cacheScope":      "partial",
		"modelVersion":    modelVersion,
		"cacheRows":       summary.Rows,
		"cacheSources":    summary.Sources,
		"impactedSources": len(impactedIDs),
	}
	emitProgress(0.98)
	return payload
}

func runRecommend(input pluginio.Input) Payload {
	modelVersion := stringArg(input.Args, "modelVersion", DefaultModelVersion)
	sourceSceneID := stringArg(input.Args, "sceneId", "")
	limit := clampLimit(input.Args["limit"], 50)
	payload := basePayload("recommend")
	payload["modelVersion"] = modelVersion
	payload["sourceSceneId"] = sourceSceneID
	payload["limit"] = limit
	payload["recommendations"] = []store.Recommendation{}
	payload["cacheHit"] = false

	if sourceSceneID == "" {
		payload["fallbackReason"] = "scene-id-required"
		return payload
	}
	dbPath, err := store.ResolveDBPath(input.PluginDir, input.Args)
	if err != nil {
		payload["fallbackReason"] = "db-path-unavailable"
		return payload
	}
	if _, err := os.Stat(dbPath); err != nil {
		payload["fallbackReason"] = "engine-db-missing"
		return payload
	}
	s, err := store.OpenReadOnly(dbPath)
	if err != nil {
		payload["fallbackReason"] = "engine-db-open-failed"
		return payload
	}
	defer s.Close()
	recommendations, err := s.Recommendations(context.Background(), modelVersion, sourceSceneID, limit)
	if err != nil {
		payload["fallbackReason"] = "recommendation-query-failed"
		return payload
	}
	if len(recommendations) == 0 {
		payload["fallbackReason"] = "cache-miss"
		return payload
	}
	payload["cacheHit"] = true
	payload["recommendations"] = recommendations
	payload["fallbackReason"] = ""
	return payload
}

func errorPayload(mode, reason, message string) Payload {
	payload := basePayload(mode)
	payload["ok"] = false
	payload["fallbackReason"] = reason
	payload["error"] = message
	return payload
}

type connection struct {
	url         string
	apiKey      string
	cookieName  string
	cookieValue string
}

func graphQLConnection(input pluginio.Input) (connection, bool) {
	url := firstString(input.Args, "stashGraphqlUrl", "stashGraphQLURL", "graphqlUrl", "graphqlURL")
	if url == "" {
		if base := firstString(input.Args, "stashBaseUrl", "stashBaseURL", "stashUrl", "stashURL", "serverUrl", "serverURL"); base != "" {
			url = appendGraphQLPath(base)
		}
	}
	if url == "" {
		url = firstString(input.ServerConnection, "GraphQLURL", "graphqlUrl", "graphqlURL", "URL", "url")
	}
	if url == "" {
		if base := firstString(input.ServerConnection, "ServerURL", "serverUrl", "serverURL", "server_url", "BaseURL", "baseUrl", "baseURL"); base != "" {
			url = appendGraphQLPath(base)
		}
	}
	if url == "" {
		if built := buildServerConnectionGraphQLURL(input.ServerConnection); built != "" {
			url = built
		}
	}
	if url == "" {
		url = os.Getenv("STASH_GRAPHQL_URL")
	}
	if url == "" {
		if base := os.Getenv("STASH_BASE_URL"); base != "" {
			url = appendGraphQLPath(base)
		}
	}
	if url == "" {
		if base := os.Getenv("STASH_URL"); base != "" {
			url = appendGraphQLPath(base)
		}
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return connection{}, false
	}
	apiKey := firstString(input.Args, "stashApiKey", "apiKey", "ApiKey")
	if apiKey == "" {
		apiKey = firstString(input.ServerConnection, "ApiKey", "apiKey", "api_key")
	}
	if apiKey == "" {
		apiKey = os.Getenv("STASH_API_KEY")
	}
	cookieName, cookieValue := serverConnectionCookie(input.ServerConnection)
	return connection{url: url, apiKey: apiKey, cookieName: cookieName, cookieValue: cookieValue}, true
}

func buildServerConnectionGraphQLURL(values map[string]any) string {
	if values == nil {
		return ""
	}
	scheme := firstString(values, "Scheme", "scheme")
	if scheme == "" {
		scheme = "http"
	}
	host := firstString(values, "Host", "host")
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	port := parseInt(values["Port"], 0)
	if port == 0 {
		port = parseInt(values["port"], 0)
	}
	if port <= 0 {
		return ""
	}
	return fmt.Sprintf("%s://%s:%d/graphql", scheme, host, port)
}

func serverConnectionCookie(values map[string]any) (string, string) {
	for _, key := range []string{"SessionCookie", "sessionCookie", "session_cookie"} {
		cookie, ok := values[key].(map[string]any)
		if !ok || cookie == nil {
			continue
		}
		name := firstString(cookie, "Name", "name")
		value := firstString(cookie, "Value", "value")
		if name != "" && value != "" {
			return name, value
		}
	}
	return "", ""
}

func appendGraphQLPath(base string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(strings.ToLower(trimmed), "/graphql") {
		return trimmed
	}
	return trimmed + "/graphql"
}

func firstString(args map[string]any, keys ...string) string {
	if args == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := args[key]; ok {
			if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func stringArg(args map[string]any, key, fallback string) string {
	if args == nil {
		return fallback
	}
	v, ok := args[key]
	if !ok || v == nil {
		return fallback
	}
	s, ok := v.(string)
	if !ok {
		return fallback
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}

func intArg(args map[string]any, key string, fallback int) int {
	if args == nil {
		return fallback
	}
	return parseInt(args[key], fallback)
}

func boolArg(args map[string]any, key string, fallback bool) bool {
	if args == nil {
		return fallback
	}
	v, ok := args[key]
	if !ok || v == nil {
		return fallback
	}
	switch value := v.(type) {
	case bool:
		return value
	case string:
		normalized := strings.TrimSpace(strings.ToLower(value))
		if normalized == "true" || normalized == "1" || normalized == "yes" {
			return true
		}
		if normalized == "false" || normalized == "0" || normalized == "no" {
			return false
		}
	}
	return fallback
}

func parseInt(value any, fallback int) int {
	var n int
	switch v := value.(type) {
	case float64:
		n = int(math.Trunc(v))
	case int:
		n = v
	case int64:
		n = int(v)
	case string:
		_, _ = fmt.Sscanf(v, "%d", &n)
	default:
		return fallback
	}
	return n
}

func clampLimit(value any, fallback int) int {
	n := parseInt(value, fallback)
	if n < 1 {
		return 1
	}
	if n > 100 {
		return 100
	}
	return n
}

func parentDir(path string) string {
	idx := strings.LastIndex(path, string(os.PathSeparator))
	if idx <= 0 {
		return "."
	}
	return path[:idx]
}

func cloneArgs(args map[string]any) map[string]any {
	cloned := map[string]any{}
	for key, value := range args {
		cloned[key] = value
	}
	return cloned
}

func copySQLiteBundle(sourcePath, targetPath string) error {
	if err := copyFile(sourcePath, targetPath, 0o600); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(sourcePath + suffix); err == nil {
			if err := copyFile(sourcePath+suffix, targetPath+suffix, 0o600); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func copyFile(sourcePath, targetPath string, mode os.FileMode) error {
	src, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return nil
}

func renameSQLiteBundle(sourcePath, targetPath string) error {
	if err := os.Rename(sourcePath, targetPath); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(sourcePath + suffix); err == nil {
			if err := os.Rename(sourcePath+suffix, targetPath+suffix); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func cleanupSQLiteBundle(path string) {
	_ = os.Remove(path)
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
}

func moveExistingDatabaseAside(dbPath, label string) (string, error) {
	info, err := os.Stat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("database path is a directory: %s", dbPath)
	}
	timestamp := time.Now().UTC().Format("20060102-150405Z")
	backupPath := fmt.Sprintf("%s.%s-backup-%s", dbPath, label, timestamp)
	if err := os.Rename(dbPath, backupPath); err != nil {
		return "", err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath + suffix); err == nil {
			_ = os.Rename(dbPath+suffix, backupPath+suffix)
		}
	}
	return backupPath, nil
}

func merge(dst Payload, src Payload) {
	for key, value := range src {
		dst[key] = value
	}
}
