# Stash Hybrid Go Engine

Integrated Stash Hybrid Recommendations raw-plugin engine.

## Scope

Implemented modes:

- `status`: returns engine capabilities and safe DB status.
- `bootstrap`: creates `{PluginDir}/data/recommendations.sqlite`, runs idempotent SQLite schema migrations, and indexes/builds the lite cache when Stash connection info is available.
- `index-scenes`: fetches Stash scene metadata through GraphQL, upserts the engine DB, and rebuilds the `hybrid-v3-lite` top-N cache.
- `recommend`: reads cached `hybrid-v3-lite` rows and returns recommendation-shaped JSON for the Stash web UI.

The Q17 recommendation model is intentionally **hybrid-v3-lite**: tags, filename tokens, duration, resolution, screenshot/thumbnail paths, and Stash-managed pHash. Full sprite/full-frame visual indexing and image processing remain out of scope.

The engine never reads or writes the existing Fastify production DB. Its default DB is always inside the engine plugin directory:

```text
{PluginDir}/data/recommendations.sqlite
```

A `dbPath` override is accepted only for tests/manual development when `allowTestDBPath=true` is present in plugin args or `STASH_HYBRID_ALLOW_DB_OVERRIDE=1` is set.

## Stash connection inputs

For `bootstrap`/`index-scenes`, pass one of these through plugin args or environment:

- `stashGraphqlUrl` / `STASH_GRAPHQL_URL` — direct GraphQL endpoint.
- `stashBaseUrl` / `STASH_BASE_URL` / `STASH_URL` — base URL; `/graphql` is appended.
- `stashApiKey` / `apiKey` / `STASH_API_KEY` — optional API key. Never print it in logs/reports.

Stash raw plugin `serverConnection` / `ServerConnection` maps are also accepted when they include common URL/API-key fields.

## Build and test

```bash
cd engine/go
go test ./...
go build ./cmd/stash-hybrid-engine
```

SQLite uses the pure-Go `modernc.org/sqlite` driver, so the normal build does not require CGO.

## Manual smoke

Schema-only bootstrap:

```bash
printf '{"PluginDir":"/tmp/StashHybridRecommendationsEngine","Args":{"mode":"bootstrap"}}' | \
  ./stash-hybrid-engine
```

Index a tiny Stash subset and build cache when a safe local Stash connection is available:

```bash
# Set STASH_API_KEY in your shell first if your Stash requires one.
printf '{"PluginDir":"/tmp/StashHybridRecommendationsEngine","Args":{"mode":"index-scenes","stashBaseUrl":"http://127.0.0.1:9999","limitScenes":100,"limit":10}}' | \
  ./stash-hybrid-engine
```

Read recommendations:

```bash
printf '{"PluginDir":"/tmp/StashHybridRecommendationsEngine","Args":{"mode":"recommend","sceneId":"1","limit":3,"modelVersion":"hybrid-v3-lite"}}' | \
  ./stash-hybrid-engine
```

The executable writes a Stash raw-plugin envelope:

```json
{"Output":"{...JSON payload...}"}
```

Public recommendation scene DTOs intentionally do not include raw pHash values.
