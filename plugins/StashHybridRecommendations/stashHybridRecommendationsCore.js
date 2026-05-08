(function (root) {
  'use strict';

  const STORAGE_PREFIX = 'stashHybridRecommendations.';
  const DEFAULT_PORT = '4174';
  const DEFAULT_MODEL_VERSION = 'hybrid-v3';
  const DEFAULT_INTEGRATED_MODEL_VERSION = 'hybrid-v3-lite';
  const DEFAULT_BACKEND_MODE = 'integrated';
  const DEFAULT_LIMIT = 15;
  const STAGED_LOCAL_PACKAGE_SOURCE = 'file:///root/.stash/package-sources/stash-hybrid-recommendations-v0.3.0/index';
  const STAGED_LOCAL_ENGINE_PACKAGE_SOURCE = 'file:///root/.stash/package-sources/stash-hybrid-recommendations-v0.3.0/engines';
  const PUBLIC_PACKAGE_SOURCE = 'https://raw.githubusercontent.com/gomeng-dev/stash-recommendation-server/main/index';
  const PUBLIC_ENGINE_PACKAGE_SOURCE = 'https://raw.githubusercontent.com/gomeng-dev/stash-recommendation-server/main/engines';
  const DEFAULT_ENGINE_PACKAGE_SOURCE = PUBLIC_ENGINE_PACKAGE_SOURCE;
  const DEFAULT_ENGINE_TARGET = 'linux-arm64v8';
  const ENGINE_TARGETS = [
    { id: 'linux-amd64', label: 'Docker / Linux amd64', packageId: 'stash-hybrid-recommendations-engine-linux-amd64', enginePluginId: 'StashHybridRecommendationsEngineLinuxAmd64' },
    { id: 'linux-arm64v8', label: 'Docker / Linux arm64 v8', packageId: 'stash-hybrid-recommendations-engine-linux-arm64v8', enginePluginId: 'StashHybridRecommendationsEngineLinuxArm64v8' },
    { id: 'linux-arm32v7', label: 'Docker / Linux arm32 v7', packageId: 'stash-hybrid-recommendations-engine-linux-arm32v7', enginePluginId: 'StashHybridRecommendationsEngineLinuxArm32v7' },
    { id: 'linux-arm32v6', label: 'Docker / Linux arm32 v6', packageId: 'stash-hybrid-recommendations-engine-linux-arm32v6', enginePluginId: 'StashHybridRecommendationsEngineLinuxArm32v6' },
    { id: 'macos-arm64', label: 'macOS Apple Silicon', packageId: 'stash-hybrid-recommendations-engine-macos-arm64', enginePluginId: 'StashHybridRecommendationsEngineMacosArm64' },
    { id: 'macos-amd64', label: 'macOS Intel', packageId: 'stash-hybrid-recommendations-engine-macos-amd64', enginePluginId: 'StashHybridRecommendationsEngineMacosAmd64' },
    { id: 'windows-amd64', label: 'Windows amd64', packageId: 'stash-hybrid-recommendations-engine-windows-amd64', enginePluginId: 'StashHybridRecommendationsEngineWindowsAmd64' },
    { id: 'windows-arm64', label: 'Windows arm64', packageId: 'stash-hybrid-recommendations-engine-windows-arm64', enginePluginId: 'StashHybridRecommendationsEngineWindowsArm64' },
    { id: 'freebsd-amd64', label: 'FreeBSD amd64', packageId: 'stash-hybrid-recommendations-engine-freebsd-amd64', enginePluginId: 'StashHybridRecommendationsEngineFreebsdAmd64' },
    { id: 'freebsd-arm64', label: 'FreeBSD arm64', packageId: 'stash-hybrid-recommendations-engine-freebsd-arm64', enginePluginId: 'StashHybridRecommendationsEngineFreebsdArm64' },
  ];

  function safeLocation(locationLike) {
    const loc = locationLike || (root && root.location) || {};
    return {
      protocol: loc.protocol || 'http:',
      hostname: loc.hostname || '127.0.0.1',
      origin: loc.origin || ((loc.protocol || 'http:') + '//' + (loc.host || loc.hostname || '127.0.0.1')),
    };
  }

  function buildDefaultBackendBase(locationLike) {
    const loc = safeLocation(locationLike);
    const protocol = loc.protocol === 'https:' ? 'https:' : 'http:';
    const hostname = loc.hostname || '127.0.0.1';
    return protocol + '//' + hostname + ':' + DEFAULT_PORT;
  }

  function normalizeBackendBase(value, locationLike) {
    const raw = typeof value === 'string' ? value.trim() : '';
    if (!raw) return buildDefaultBackendBase(locationLike);
    return raw.replace(/\/+$/, '');
  }

  function clampLimit(value) {
    const parsed = Number.parseInt(String(value == null ? '' : value), 10);
    if (!Number.isFinite(parsed) || parsed <= 0) return DEFAULT_LIMIT;
    return Math.max(1, Math.min(parsed, 100));
  }

  function normalizeModelVersion(value) {
    const raw = typeof value === 'string' ? value.trim().toLowerCase() : '';
    return /^hybrid-v\d+(?:-[a-z0-9._]+)*$/.test(raw) ? raw : DEFAULT_MODEL_VERSION;
  }

  function normalizeBackendMode(value) {
    const raw = typeof value === 'string' ? value.trim().toLowerCase() : '';
    return raw === 'integrated' || raw === 'api' ? raw : DEFAULT_BACKEND_MODE;
  }

  function buildRecommendationsUrl(base, sceneId, limit, modelVersion) {
    const normalizedBase = normalizeBackendBase(base);
    const path = '/api/scenes/' + encodeURIComponent(String(sceneId)) + '/recommendations';
    const params = new URLSearchParams();
    params.set('limit', String(clampLimit(limit)));
    params.set('modelVersion', normalizeModelVersion(modelVersion));
    return normalizedBase + path + '?' + params.toString();
  }

  function rewriteStashAssetUrl(url, locationLike) {
    if (typeof url !== 'string') return '';
    const trimmed = url.trim();
    if (!trimmed) return '';
    try {
      const loc = safeLocation(locationLike);
      const parsed = new URL(trimmed, loc.origin);
      if ((parsed.hostname === 'localhost' || parsed.hostname === '127.0.0.1') && parsed.pathname.startsWith('/scene/')) {
        return loc.origin.replace(/\/+$/, '') + parsed.pathname + parsed.search + parsed.hash;
      }
      return parsed.toString();
    } catch (_) {
      return trimmed;
    }
  }

  function pickSceneImage(scene, locationLike) {
    const candidate = scene && (scene.thumbnailUrl || scene.spriteImageUrl);
    return rewriteStashAssetUrl(candidate || '', locationLike);
  }

  function formatDuration(seconds) {
    const total = Math.max(0, Math.round(Number(seconds) || 0));
    const h = Math.floor(total / 3600);
    const m = Math.floor((total % 3600) / 60);
    const s = total % 60;
    if (h > 0) return h + ':' + String(m).padStart(2, '0') + ':' + String(s).padStart(2, '0');
    return m + ':' + String(s).padStart(2, '0');
  }

  function formatPercent(score) {
    const n = Number(score);
    if (!Number.isFinite(n)) return '—';
    return Math.round(Math.max(0, Math.min(1, n)) * 100) + '%';
  }

  function normalizeEngineTarget(value) {
    const raw = String(value == null ? '' : value).trim().toLowerCase();
    const compact = raw.replace(/docker|direct|native|\/|_/g, ' ').replace(/\s+/g, ' ').trim();
    const canonical = compact
      .replace(/^macos arm64$/, 'macos-arm64')
      .replace(/^macos amd64$/, 'macos-amd64')
      .replace(/^linux amd64$/, 'linux-amd64')
      .replace(/^linux arm64 v?8$/, 'linux-arm64v8')
      .replace(/^linux arm32 v?7$/, 'linux-arm32v7')
      .replace(/^linux arm32 v?6$/, 'linux-arm32v6')
      .replace(/^windows amd64$/, 'windows-amd64')
      .replace(/^windows arm64$/, 'windows-arm64')
      .replace(/^freebsd amd64$/, 'freebsd-amd64')
      .replace(/^freebsd arm64$/, 'freebsd-arm64');
    const found = ENGINE_TARGETS.find(function (target) {
      return target.id === raw || target.id === canonical || target.label.toLowerCase() === raw;
    });
    return found ? found.id : DEFAULT_ENGINE_TARGET;
  }

  function getEngineTarget(value) {
    const id = normalizeEngineTarget(value);
    return ENGINE_TARGETS.find(function (target) { return target.id === id; }) || ENGINE_TARGETS[0];
  }

  function inferEngineTarget(env) {
    const source = env || {};
    const platform = String(source.platform || '').toLowerCase();
    const ua = String(source.userAgent || '').toLowerCase();
    const combined = platform + ' ' + ua;
    if (combined.includes('win')) return combined.includes('arm64') || combined.includes('aarch64') ? 'windows-arm64' : 'windows-amd64';
    if (combined.includes('mac') || combined.includes('darwin')) {
      return combined.includes('arm') || combined.includes('apple silicon') || platform === 'macarm' ? 'macos-arm64' : 'macos-amd64';
    }
    if (combined.includes('freebsd')) return combined.includes('arm64') || combined.includes('aarch64') ? 'freebsd-arm64' : 'freebsd-amd64';
    if (combined.includes('aarch64') || combined.includes('arm64')) return 'linux-arm64v8';
    if (combined.includes('armv6')) return 'linux-arm32v6';
    if (combined.includes('armv7') || combined.includes('arm')) return 'linux-arm32v7';
    return DEFAULT_ENGINE_TARGET;
  }

  function normalizePackageSourceUrl(value) {
    const raw = typeof value === 'string' ? value.trim() : '';
    if (!raw) return DEFAULT_ENGINE_PACKAGE_SOURCE;
    if (raw === STAGED_LOCAL_PACKAGE_SOURCE) return STAGED_LOCAL_ENGINE_PACKAGE_SOURCE;
    if (raw === 'file:///root/.stash/package-sources/stash-hybrid-recommendations-v0.3.0/index.yml') return STAGED_LOCAL_ENGINE_PACKAGE_SOURCE;
    if (raw === 'file:///root/.stash/package-sources/stash-hybrid-recommendations-v0.3.0/engines.yml') return STAGED_LOCAL_ENGINE_PACKAGE_SOURCE;
    if (raw === PUBLIC_PACKAGE_SOURCE) return PUBLIC_ENGINE_PACKAGE_SOURCE;
    if (raw === 'https://github.com/gomeng-dev/stash-recommendation-server/releases/download/stash-hybrid-recommendations-v0.3.0/index.yml') return PUBLIC_ENGINE_PACKAGE_SOURCE;
    if (raw === 'https://github.com/gomeng-dev/stash-recommendation-server/releases/download/stash-hybrid-recommendations-v0.3.0/engines.yml') return PUBLIC_ENGINE_PACKAGE_SOURCE;
    return raw;
  }

  function buildEnginePackageSpec(target, sourceURL) {
    const engine = getEngineTarget(target);
    return { id: engine.packageId, sourceURL: normalizePackageSourceUrl(sourceURL) };
  }

  function buildInstallPackagesMutation() {
    return 'mutation InstallHybridEngine($packages: [PackageSpecInput!]!) { installPackages(type: Plugin, packages: $packages) }';
  }

  function buildReloadPluginsMutation() {
    return 'mutation ReloadHybridPlugins { reloadPlugins }';
  }

  function buildSetPluginsEnabledMutation() {
    return 'mutation EnableHybridEngine($enabledMap: BoolMap!) { setPluginsEnabled(enabledMap: $enabledMap) }';
  }

  function buildRunPluginTaskMutation() {
    return 'mutation RunHybridPluginTask($plugin_id: ID!, $task_name: String, $args: Map) { runPluginTask(plugin_id: $plugin_id, task_name: $task_name, args_map: $args) }';
  }

  function buildRunPluginOperationMutation() {
    return 'mutation RunHybridPluginOperation($plugin_id: ID!, $args: Map) { runPluginOperation(plugin_id: $plugin_id, args: $args) }';
  }

  function buildPluginsQuery() {
    return 'query HybridPlugins { plugins { id name enabled tasks { name } } }';
  }

  function buildSceneCountQuery() {
    return 'query HybridSceneCount { findScenes(filter: { page: 1, per_page: 1 }) { count } }';
  }

  function buildRunPluginTaskVariables(pluginId, taskName, args) {
    return { plugin_id: String(pluginId || ''), task_name: String(taskName || ''), args: args || {} };
  }

  function buildRunPluginOperationVariables(pluginId, args) {
    return { plugin_id: String(pluginId || ''), args: args || {} };
  }

  function buildRecommendOperationArgs(sceneId, limit) {
    return {
      mode: 'recommend',
      sceneId: String(sceneId || ''),
      limit: clampLimit(limit),
    };
  }

  function buildBootstrapTaskArgs() {
    return {
      mode: 'bootstrap',
    };
  }

  function buildIndexScenesTaskArgs() {
    return {
      mode: 'index-scenes',
    };
  }

  function buildCacheTaskArgs() {
    return {
      mode: 'build-cache',
    };
  }

  function buildPruneDeletedScenesTaskArgs() {
    return {
      mode: 'prune-deleted-scenes',
    };
  }

  function buildImportDbTaskArgs(sourceDbPath) {
    return {
      mode: 'import-db',
      sourceDbPath: String(sourceDbPath || '').trim(),
    };
  }

  function buildDevTest100TaskArgs() {
    return {
      mode: 'dev-test-100',
      maxScenes: 100,
      limitScenes: 100,
      topN: 50,
    };
  }

  function buildRecommendOperationVariables(pluginId, sceneId, limit) {
    return buildRunPluginOperationVariables(pluginId, buildRecommendOperationArgs(sceneId, limit));
  }

  function parsePluginOperationOutput(value) {
    if (value == null) return {};
    if (typeof value === 'string') {
      const trimmed = value.trim();
      if (!trimmed) return {};
      try {
        return JSON.parse(trimmed);
      } catch (_) {
        return { ok: true, output: trimmed };
      }
    }
    if (typeof value === 'object') {
      if (Object.prototype.hasOwnProperty.call(value, 'runPluginOperation')) return parsePluginOperationOutput(value.runPluginOperation);
      if (typeof value.Output === 'string') return parsePluginOperationOutput(value.Output);
      if (typeof value.output === 'string') return parsePluginOperationOutput(value.output);
      return value;
    }
    return { ok: true, output: String(value) };
  }

  function normalizeRecommendationResponse(payload, options) {
    const source = parsePluginOperationOutput(payload);
    const opts = options || {};
    const recommendations = Array.isArray(source.recommendations) ? source.recommendations : [];
    return {
      ok: source.ok !== false,
      mode: source.mode || opts.mode || 'recommend',
      modelVersion: normalizeModelVersion(source.modelVersion || opts.modelVersion || DEFAULT_INTEGRATED_MODEL_VERSION),
      cacheHit: Boolean(source.cacheHit),
      fallbackReason: source.fallbackReason || opts.fallbackReason || '',
      sourceSceneId: String(source.sourceSceneId || opts.sourceSceneId || ''),
      recommendations: recommendations.map(function (rec) {
        const scene = rec && rec.scene && typeof rec.scene === 'object' ? rec.scene : {};
        const sceneId = String((rec && rec.sceneId) || scene.id || '');
        return {
          sceneId: sceneId,
          score: Number.isFinite(Number(rec && rec.score)) ? Math.max(0, Math.min(1, Number(rec.score))) : 0,
          reasons: Array.isArray(rec && rec.reasons) ? rec.reasons.map(String) : [],
          breakdown: rec && rec.breakdown && typeof rec.breakdown === 'object' ? rec.breakdown : {},
          weights: rec && rec.weights && typeof rec.weights === 'object' ? rec.weights : {},
          scene: Object.assign({}, scene, { id: scene.id || sceneId }),
        };
      }),
    };
  }

  function extractGraphQLErrorMessage(payload) {
    if (!payload) return 'Empty response.';
    if (Array.isArray(payload.errors) && payload.errors.length) {
      return payload.errors.map(function (err) { return err && err.message ? err.message : String(err); }).join('; ');
    }
    return '';
  }

  function readSetting(key, fallback) {
    try {
      const raw = root.localStorage && root.localStorage.getItem(STORAGE_PREFIX + key);
      return raw == null || raw === '' ? fallback : raw;
    } catch (_) {
      return fallback;
    }
  }

  function writeSetting(key, value) {
    try {
      if (root.localStorage) root.localStorage.setItem(STORAGE_PREFIX + key, String(value));
    } catch (_) {}
  }

  root.StashHybridRecommendationsCore = {
    STORAGE_PREFIX,
    DEFAULT_PORT,
    DEFAULT_MODEL_VERSION,
    DEFAULT_INTEGRATED_MODEL_VERSION,
    DEFAULT_BACKEND_MODE,
    DEFAULT_LIMIT,
    DEFAULT_ENGINE_PACKAGE_SOURCE,
    PUBLIC_PACKAGE_SOURCE,
    PUBLIC_ENGINE_PACKAGE_SOURCE,
    DEFAULT_ENGINE_TARGET,
    ENGINE_TARGETS,
    buildDefaultBackendBase,
    normalizeBackendBase,
    clampLimit,
    normalizeModelVersion,
    normalizeBackendMode,
    buildRecommendationsUrl,
    rewriteStashAssetUrl,
    pickSceneImage,
    formatDuration,
    formatPercent,
    normalizeEngineTarget,
    getEngineTarget,
    inferEngineTarget,
    normalizePackageSourceUrl,
    buildEnginePackageSpec,
    buildInstallPackagesMutation,
    buildReloadPluginsMutation,
    buildSetPluginsEnabledMutation,
    buildRunPluginTaskMutation,
    buildRunPluginOperationMutation,
    buildPluginsQuery,
    buildSceneCountQuery,
    buildRunPluginTaskVariables,
    buildRunPluginOperationVariables,
    buildRecommendOperationArgs,
    buildBootstrapTaskArgs,
    buildIndexScenesTaskArgs,
    buildCacheTaskArgs,
    buildPruneDeletedScenesTaskArgs,
    buildImportDbTaskArgs,
    buildDevTest100TaskArgs,
    buildRecommendOperationVariables,
    parsePluginOperationOutput,
    normalizeRecommendationResponse,
    extractGraphQLErrorMessage,
    readSetting,
    writeSetting,
  };
})(typeof window !== 'undefined' ? window : globalThis);
