# Stash Hybrid Recommendations

Local, private recommendation tabs for [Stash](https://stashapp.cc/) scene pages.

This project ships as a Stash plugin plus a managed native engine. The UI plugin adds a **Hybrid Recommendations** tab to scene detail pages and a setup panel under **Settings -> Plugins**. The engine builds a local SQLite recommendation cache from your own Stash metadata and pHash fingerprints, then serves recommendations through Stash's plugin operation API. No separate recommendation web server is required for the integrated plugin flow.

## What You Get

- **Scene-page recommendations**: a new `Hybrid Recommendations` tab on Stash scene pages.
- **In-Stash setup flow**: install/enable the native engine from the plugin settings page.
- **Private local cache**: recommendations are stored in an SQLite DB under the installed engine plugin directory.
- **Docker-friendly engine packages**: choose the OS/architecture where Stash is running, not necessarily your desktop OS.
- **Incremental scene indexing**: already-indexed scenes with the same Stash `updated_at` value are skipped.
- **Cache rebuild controls**: run a full DB build, metadata-only index, cache-only rebuild, or automatic sync for new scenes.
- **Existing DB import**: import a readable recommendation SQLite DB path visible to the engine process.
- **No raw pHash exposure**: pHash fingerprints are used internally but are not returned in public recommendation scene objects.

## Current Status

- Plugin package version: `0.3.0`
- Default model: `hybrid-v3-lite`
- Default recommendation count in the tab: `15`
- Supported engine targets:
  - `linux-amd64`
  - `linux-arm64v8`
  - `linux-arm32v7`
  - `linux-arm32v6`
  - `macos-arm64`
  - `macos-amd64`
  - `windows-amd64`
  - `windows-arm64`
  - `freebsd-amd64`
  - `freebsd-arm64`

The current integrated Go engine focuses on the lightweight `hybrid-v3-lite` path: Stash metadata, filenames, duration/resolution, thumbnails/sprites, and Stash-managed pHash. The older full sprite/full-frame visual index is not required by the integrated plugin and has not been ported into the Go engine yet.

## Quick Install

1. In Stash, open **Settings -> Plugins**.
2. Add this package source:

   ```text
   https://raw.githubusercontent.com/gomeng-dev/stash-recommendation-server/main/index
   ```

3. Install **Stash Hybrid Recommendations**.
4. Open **Settings -> Plugins -> Installed Plugins -> Stash Hybrid Recommendations**.
5. Select and install the engine package for the machine/container where Stash runs.
6. Click **Enable engine** if the engine is installed but disabled.
7. Click **Preflight** to confirm Stash can call the engine.
8. Click **Build DB** and watch Stash's task progress.
9. Open any scene page and select the **Hybrid Recommendations** tab.

Only add the public `index` package source manually. The onboarding plugin uses the sibling `engines` source internally when installing the selected native engine.

## Choosing the Right Engine Target

Choose the target for the **Stash runtime environment**.

| Stash install type | Recommended target |
| --- | --- |
| Docker on Intel/AMD NAS, server, or PC | `Docker / Linux amd64` (`linux-amd64`) |
| Docker on Apple Silicon Mac or ARM64 server | `Docker / Linux arm64 v8` (`linux-arm64v8`) |
| Docker on Raspberry Pi 4/5 or 64-bit ARM SBC | usually `linux-arm64v8` |
| Docker on older 32-bit ARM boards | `linux-arm32v7` or `linux-arm32v6` |
| Native macOS on Apple Silicon | `macOS Apple Silicon` (`macos-arm64`) |
| Native macOS on Intel | `macOS Intel` (`macos-amd64`) |
| Native Windows | `windows-amd64` or `windows-arm64` |
| Native FreeBSD | `freebsd-amd64` or `freebsd-arm64` |

Important Docker rule: if Stash runs in Docker on macOS or Windows, you almost always need a **Linux** engine package, because the engine executes inside the Stash/container environment.

## How the Integrated Plugin Works

The plugin is split into two layers:

```text
Stash Hybrid Recommendations
  -> UI plugin loaded by Stash
  -> setup/onboarding panel in Settings -> Plugins
  -> Hybrid Recommendations tab on scene pages

Stash Hybrid Recommendations Engine - <target>
  -> native Stash raw plugin for the selected OS/architecture
  -> SQLite recommendation DB under its plugin data directory
  -> status, recommend, index, cache, and import tasks
```

Runtime flow:

```text
Scene page
  -> Hybrid Recommendations tab
  -> Stash GraphQL runPluginOperation
  -> selected native engine plugin
  -> local SQLite recommendations.sqlite
  -> recommendation cards in the Stash UI
```

Build/index flow:

```text
Settings -> Plugins setup panel
  -> Stash GraphQL runPluginTask
  -> selected native engine plugin
  -> Stash GraphQL scene metadata + pHash fetch
  -> local SQLite scenes table
  -> hybrid-v3-lite recommendation cache
```

The integrated path avoids CORS, mixed-content, public ports, and browser-side API-key handling because the recommendation engine runs as a Stash plugin instead of as a separate HTTP service.

## Engine Database

The engine creates and reads its own database in the installed engine plugin directory:

```text
{EnginePluginDir}/data/recommendations.sqlite
```

For package-installed plugins, the exact directory is managed by Stash and may vary by package source URL. Do not assume it is always directly under `plugins/<package-id>/`; search for the installed engine plugin directory if you need to inspect the DB manually.

The engine DB contains:

- Stash scene IDs and safe scene metadata.
- Tags and filename-derived tokens used for matching.
- Thumbnail/sprite URLs returned by Stash.
- Normalized internal pHash values where available.
- Cached top-N recommendation rows for `hybrid-v3-lite`.
- JSON score breakdowns and weights used to explain each recommendation.

The engine does **not** need the older Fastify `:4174` service or its historical full-frame visual SQLite DB for the integrated `hybrid-v3-lite` flow.

## Setup Panel Actions

The `Stash Hybrid Recommendations` settings panel includes these actions:

- **Install engine**: installs the selected OS/architecture engine package through Stash's package manager.
- **Enable engine**: enables the installed engine plugin if Stash installed it disabled.
- **Preflight**: calls the engine status operation and checks whether the selected engine is usable.
- **Build DB**: creates/migrates the engine DB, indexes scenes, and builds the `hybrid-v3-lite` recommendation cache.
- **Index scenes**: refreshes local scene metadata from Stash. Unchanged scenes are skipped when their Stash `updated_at` value has not changed.
- **Rebuild cache**: rebuilds recommendation rows from already-indexed scenes and current scoring weights.
- **Prune deleted scenes**: fetches the current Stash scene ID set, removes scenes that no longer exist in Stash, deletes stale recommendation rows, and rebuilds only affected source-scene cache rows.
- **Auto sync Stash scene additions/deletions**: starts incremental indexing when Stash has more scenes than the DB, or starts a deleted-scene prune when the DB has more scenes than Stash.
- **Import DB file**: backs up the current engine DB, imports a readable SQLite DB file, then migrates it if needed.
- **Build dev test DB (100 scenes)**: development-only smoke task. It moves aside the active DB and builds a small 100-scene DB. Do not use it on a valuable full DB unless you have a backup.

Stash long-running tasks can be monitored from **Settings -> Tasks**.

## Recommendation Tab

On a scene page, the plugin adds a **Hybrid Recommendations** tab.

The tab:

- Calls the integrated engine with `mode: recommend`.
- Uses the engine's default model instead of exposing model selection in the scene UI.
- Lets you choose only the output limit (`1..100`, default `15`).
- Shows recommendation cards with score, reasons, and thumbnails when available.
- Rewrites relative/local Stash asset URLs so thumbnails work from the current Stash web origin.
- Shows a setup hint if the engine is missing, disabled, or has no recommendation cache.

## Scoring Model

The current model is `hybrid-v3-lite`.

It combines these signals:

- **Tags**: overlap between normalized Stash tags.
- **Filename**: token overlap after basic cleanup of common video/file words.
- **Visual-family score**: a lightweight visual/pHash-oriented similarity signal in the integrated engine.
- **pHash**: Stash-managed perceptual hash similarity for valid 16-character hex pHash values.
- **Duration**: duration similarity.
- **Resolution**: width/height similarity.
- **Behavior**: reserved behavior/reranking slot.

The model uses adaptive weights depending on whether the source scene has enough metadata:

| Source scene profile | Tag | Filename | Visual | pHash | Duration | Resolution | Behavior |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Metadata-rich | 0.264 | 0.185 | 0.340 | 0.066 | 0.092 | 0.040 | 0.013 |
| Tag-sparse | 0.119 | 0.194 | 0.460 | 0.076 | 0.097 | 0.032 | 0.022 |

Metadata-rich scenes keep more weight on tags and filenames. Tag-sparse scenes lean harder on visual-family/pHash-style matching so scenes with weak metadata can still get useful recommendations.

## Privacy and Data Handling

- Recommendations are computed from your own Stash data.
- The engine DB is local to your Stash/plugin environment.
- The plugin does not require a third-party recommendation API.
- Raw pHash fingerprints are kept internal and are not exposed in recommendation response scene DTOs.
- The UI talks to Stash's plugin APIs rather than sending media metadata to an external service.
- Package installation still downloads plugin/engine packages from the configured package source, so use a source you trust.

## Updating

When a new version is released:

1. Open **Settings -> Plugins**.
2. Refresh the package source if Stash does not show the update automatically.
3. Update **Stash Hybrid Recommendations** if offered.
4. Update the installed engine package for your selected target.
5. Reload plugins if Stash asks you to.
6. Hard-refresh the browser tab if old plugin UI text is still visible.
7. Run **Preflight**.
8. Run **Rebuild cache** if the release changed scoring logic or the engine asks for a cache rebuild.

If the package-source accordion opens but shows no available rows after the onboarding plugin is already installed, that can be normal: Stash filters out packages that are already installed. Check **Installed Plugins** instead.

## Package Sources

Public onboarding source to add in Stash:

```text
https://raw.githubusercontent.com/gomeng-dev/stash-recommendation-server/main/index
```

Managed engine source used by the onboarding plugin:

```text
https://raw.githubusercontent.com/gomeng-dev/stash-recommendation-server/main/engines
```

The public source intentionally lists only the onboarding UI package. The engine source lists native engine packages and is normally used only by the setup panel.

## Troubleshooting

### The engine install starts but nothing changes

- Open **Settings -> Tasks** and check the package install job.
- Click **Reload Plugins** in Stash after the install job finishes.
- Return to **Installed Plugins -> Stash Hybrid Recommendations** and click **Preflight**.
- Make sure the selected engine plugin is enabled.

### Preflight says the engine is missing

- Confirm you installed an engine package, not just the UI plugin.
- Confirm you selected the target for the Stash runtime environment.
- Docker installs usually need a Linux target even on macOS/Windows hosts.
- Reload plugins and enable the engine plugin if it is present but disabled.

### Build DB finishes too quickly or produces no recommendations

- Check Stash task logs for the engine job.
- Confirm the engine can reach the Stash GraphQL API through the plugin-provided server connection.
- Run **Preflight** and check whether the database exists and has scenes.
- Try **Index scenes** first, then **Rebuild cache**.

### Recommendations tab shows no results

- Confirm **Build DB** completed successfully.
- Run **Preflight** and verify the engine reports a scene count and recommendation cache.
- Try a scene that has tags, filename metadata, duration, and a generated Stash pHash.
- Increase the tab limit if you are testing edge cases.

### Thumbnails do not load

- Confirm Stash itself shows screenshots/thumbnails for the scene.
- Open Stash from the same origin you normally use; the plugin rewrites local/relative asset URLs through the current Stash web origin.
- If Stash authentication is required, make sure the browser session is logged in.

### I chose the wrong engine target

- Install the correct engine target from the setup panel.
- Reload plugins.
- Enable the correct engine plugin.
- Run **Preflight** again.
- If you built a DB under the wrong engine plugin, back it up before removing or reinstalling packages.

### I need to import an existing DB

Use **Import DB file** in the setup panel and enter a path that the engine process can read.

For Docker installs, this must be the path as seen inside the container, not necessarily the host path. The engine backs up the current DB before activating the imported file.

## Development

Requirements:

- Node.js `>=22`
- Go toolchain for the native engine
- Python 3 for package build/validation scripts

Common commands from the repo root:

```bash
npm test
npm run test:go
npm run test:all
npm run secret:scan
npm run package:stash
npm run package:stash:validate
```

Build and test only the Go engine:

```bash
cd engine/go
go test ./...
go build ./cmd/stash-hybrid-engine
```

The package builder creates:

```text
plugins/StashHybridRecommendations/package-source/index.yml
plugins/StashHybridRecommendations/package-source/index
plugins/StashHybridRecommendations/package-source/engines.yml
plugins/StashHybridRecommendations/package-source/engines
plugins/StashHybridRecommendations/package-source/zips/stash-hybrid-recommendations.zip
plugins/StashHybridRecommendations/package-source/zips/stash-hybrid-recommendations-engine-<target>.zip
```

## Public Release Contents

This public repository is generated from a private development repository. The public release tree includes source and release assets needed by Stash:

```text
README.md
package.json
package-lock.json
engine/go/**
plugins/StashHybridRecommendations/**
scripts/*.py
scripts/*.mjs
index
index.yml
engines
engines.yml
release-manifest.json
zips/*.zip
```

Private development notes, prompts, agent files, local databases, logs, `.env` files, and queue/planning documents are intentionally excluded from the public release tree.

## Limitations

- The integrated Go engine currently implements `hybrid-v3-lite`, not the older full-frame sprite/dHash visual index.
- Recommendation quality depends on available Stash metadata and pHash coverage.
- FreeBSD arm32 is not packaged; FreeBSD amd64 and arm64 are packaged.
- Engine package installation depends on Stash's package manager and the package source being reachable from the Stash server/container.
- The dev 100-scene DB task is for smoke testing and intentionally replaces the active DB after moving it aside.
