import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const context = {
  console,
  URL,
  URLSearchParams,
  window: {},
  globalThis: {},
};
context.window = context;
context.globalThis = context;
vm.createContext(context);
vm.runInContext(readFileSync(new URL('../stashHybridRecommendationsCore.js', import.meta.url), 'utf8'), context);

const core = context.StashHybridRecommendationsCore;

assert.equal(
  core.buildDefaultBackendBase({ protocol: 'http:', hostname: '192.168.0.216' }),
  'http://192.168.0.216:4174'
);

assert.equal(
  core.normalizeBackendBase('', { protocol: 'http:', hostname: 'stash.local' }),
  'http://stash.local:4174'
);

assert.equal(
  core.normalizeBackendBase('http://server:4174/', { protocol: 'http:', hostname: 'stash.local' }),
  'http://server:4174'
);

assert.equal(
  core.buildRecommendationsUrl('http://server:4174', 'abc/123', 12, 'hybrid-v3'),
  'http://server:4174/api/scenes/abc%2F123/recommendations?limit=12&modelVersion=hybrid-v3'
);

assert.equal(
  core.rewriteStashAssetUrl('http://localhost:9999/scene/42/screenshot?t=1', { origin: 'http://192.168.0.216:9999' }),
  'http://192.168.0.216:9999/scene/42/screenshot?t=1'
);

assert.equal(
  core.pickSceneImage({ thumbnailUrl: ' http://127.0.0.1:9999/scene/1/screenshot ' }, { origin: 'http://stash:9999' }),
  'http://stash:9999/scene/1/screenshot'
);

assert.equal(
  core.pickSceneImage({ spriteVttUrl: 'http://127.0.0.1:9999/scene/1/sprite.vtt' }, { origin: 'http://stash:9999' }),
  ''
);

assert.equal(core.formatDuration(3661), '1:01:01');
assert.equal(core.formatPercent(0.876), '88%');
assert.equal(core.normalizeBackendMode('integrated'), 'integrated');
assert.equal(core.normalizeBackendMode('api'), 'api');
assert.equal(core.normalizeBackendMode('auto'), 'integrated');
assert.equal(core.normalizeBackendMode('bad-mode'), 'integrated');

assert.equal(core.normalizeEngineTarget('Docker / Linux amd64'), 'linux-amd64');
assert.equal(core.normalizeEngineTarget('linux arm64 v8'), 'linux-arm64v8');
assert.equal(core.normalizeEngineTarget('macos-arm64'), 'macos-arm64');
assert.equal(core.normalizeEngineTarget('unknown-target'), 'linux-arm64v8');
assert.equal(core.PUBLIC_PACKAGE_SOURCE, 'https://raw.githubusercontent.com/gomeng-dev/stash-recommendation-server/main/index');
assert.equal(core.PUBLIC_ENGINE_PACKAGE_SOURCE, 'https://raw.githubusercontent.com/gomeng-dev/stash-recommendation-server/main/engines');
assert.equal(core.DEFAULT_ENGINE_PACKAGE_SOURCE, core.PUBLIC_ENGINE_PACKAGE_SOURCE);
assert.equal(
  core.normalizePackageSourceUrl('file:///root/.stash/package-sources/stash-hybrid-recommendations-v0.3.0/index.yml'),
  'file:///root/.stash/package-sources/stash-hybrid-recommendations-v0.3.0/engines'
);
assert.equal(
  core.normalizePackageSourceUrl(core.PUBLIC_PACKAGE_SOURCE),
  core.PUBLIC_ENGINE_PACKAGE_SOURCE
);
assert.equal(
  core.buildEnginePackageSpec('linux-amd64', 'file:///root/.stash/package-sources/stash-hybrid-recommendations-v0.3.0/index.yml').sourceURL,
  'file:///root/.stash/package-sources/stash-hybrid-recommendations-v0.3.0/engines'
);

assert.deepEqual(JSON.parse(JSON.stringify(core.getEngineTarget('linux-arm64v8'))), {
  id: 'linux-arm64v8',
  label: 'Docker / Linux arm64 v8',
  packageId: 'stash-hybrid-recommendations-engine-linux-arm64v8',
  enginePluginId: 'StashHybridRecommendationsEngineLinuxArm64v8',
});

assert.equal(
  core.inferEngineTarget({ userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)', platform: 'MacIntel' }),
  'macos-amd64'
);
assert.equal(
  core.inferEngineTarget({ userAgent: 'Mozilla/5.0 (Macintosh; Apple Silicon)', platform: 'MacARM' }),
  'macos-arm64'
);
assert.equal(
  core.inferEngineTarget({ userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64)', platform: 'Win32' }),
  'windows-amd64'
);

assert.deepEqual(
  JSON.parse(JSON.stringify(core.buildEnginePackageSpec('linux-amd64', 'https://example.test/index.yml'))),
  { id: 'stash-hybrid-recommendations-engine-linux-amd64', sourceURL: 'https://example.test/index.yml' }
);

assert.equal(
  core.buildInstallPackagesMutation().replace(/\s+/g, ' ').trim(),
  'mutation InstallHybridEngine($packages: [PackageSpecInput!]!) { installPackages(type: Plugin, packages: $packages) }'
);
assert.equal(
  core.buildRunPluginOperationMutation().replace(/\s+/g, ' ').trim(),
  'mutation RunHybridPluginOperation($plugin_id: ID!, $args: Map) { runPluginOperation(plugin_id: $plugin_id, args: $args) }'
);
assert.equal(
  core.buildPluginsQuery().replace(/\s+/g, ' ').trim(),
  'query HybridPlugins { plugins { id name enabled tasks { name } } }'
);
assert.equal(
  core.buildSceneCountQuery().replace(/\s+/g, ' ').trim(),
  'query HybridSceneCount { findScenes(filter: { page: 1, per_page: 1 }) { count } }'
);
assert.deepEqual(JSON.parse(JSON.stringify(core.buildRunPluginTaskVariables('PluginA', 'Bootstrap recommendation DB', { mode: 'bootstrap' }))), {
  plugin_id: 'PluginA',
  task_name: 'Bootstrap recommendation DB',
  args: { mode: 'bootstrap' },
});
assert.equal(core.DEFAULT_LIMIT, 15);
assert.deepEqual(JSON.parse(JSON.stringify(core.buildRecommendOperationArgs('scene-1', 150, 'hybrid-v3-lite'))), {
  mode: 'recommend',
  sceneId: 'scene-1',
  limit: 100,
});
assert.deepEqual(JSON.parse(JSON.stringify(core.buildBootstrapTaskArgs())), {
  mode: 'bootstrap',
});
assert.deepEqual(JSON.parse(JSON.stringify(core.buildIndexScenesTaskArgs())), {
  mode: 'index-scenes',
});
assert.deepEqual(JSON.parse(JSON.stringify(core.buildCacheTaskArgs())), {
  mode: 'build-cache',
});
assert.deepEqual(JSON.parse(JSON.stringify(core.buildPruneDeletedScenesTaskArgs())), {
  mode: 'prune-deleted-scenes',
});
assert.deepEqual(JSON.parse(JSON.stringify(core.buildImportDbTaskArgs(' /data/old.sqlite '))), {
  mode: 'import-db',
  sourceDbPath: '/data/old.sqlite',
});
assert.deepEqual(JSON.parse(JSON.stringify(core.buildDevTest100TaskArgs())), {
  mode: 'dev-test-100',
  maxScenes: 100,
  limitScenes: 100,
  topN: 50,
});
assert.deepEqual(JSON.parse(JSON.stringify(core.buildRecommendOperationVariables('EnginePlugin', 'scene-1', 3, 'hybrid-v3-lite-dummy'))), {
  plugin_id: 'EnginePlugin',
  args: {
    mode: 'recommend',
    sceneId: 'scene-1',
    limit: 3,
  },
});
const normalizedOperation = core.normalizeRecommendationResponse({
  Output: JSON.stringify({
    ok: true,
    mode: 'recommend',
    modelVersion: 'hybrid-v3-lite-dummy',
    cacheHit: false,
    sourceSceneId: 'source-1',
    recommendations: [{ sceneId: 'rec-1', score: 1.5, reasons: ['dummy'], scene: { title: 'Dummy scene' } }],
  }),
});
assert.equal(normalizedOperation.modelVersion, 'hybrid-v3-lite-dummy');
assert.equal(normalizedOperation.sourceSceneId, 'source-1');
assert.equal(normalizedOperation.recommendations.length, 1);
assert.equal(normalizedOperation.recommendations[0].score, 1);
assert.equal(normalizedOperation.recommendations[0].scene.id, 'rec-1');
assert.equal(JSON.stringify(normalizedOperation.recommendations[0].reasons), JSON.stringify(['dummy']));
assert.equal(core.extractGraphQLErrorMessage({ errors: [{ message: 'boom' }] }), 'boom');
assert.equal(core.extractGraphQLErrorMessage({ data: { installPackages: 'job-1' } }), '');

console.log('core tests passed');
