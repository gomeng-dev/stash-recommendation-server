(function () {
  'use strict';

  const PLUGIN_ID = 'StashHybridRecommendations';
  const TAB_KEY = 'stash-hybrid-recommendations-tab';
  const logPrefix = '[StashHybridRecommendations]';

  function waitForPluginApi(attempt) {
    const w = window;
    if (w.PluginApi && w.PluginApi.React && w.StashHybridRecommendationsCore) {
      initialize(w.PluginApi, w.StashHybridRecommendationsCore);
      return;
    }
    if ((attempt || 0) < 80) {
      window.setTimeout(function () { waitForPluginApi((attempt || 0) + 1); }, 100);
    } else {
      console.warn(logPrefix, 'PluginApi/Core not available; plugin not initialized');
    }
  }

  function initialize(PluginApi, Core) {
    const React = PluginApi.React;
    const Bootstrap = PluginApi.libraries && PluginApi.libraries.Bootstrap ? PluginApi.libraries.Bootstrap : {};
    const Nav = Bootstrap.Nav;
    const Tab = Bootstrap.Tab;
    const Button = Bootstrap.Button || fallbackButton(React);
    const Form = Bootstrap.Form || fallbackForm(React);
    const Alert = Bootstrap.Alert || fallbackAlert(React);
    const Spinner = Bootstrap.Spinner || fallbackSpinner(React);

    if (!React || !React.useEffect || !React.useState || !PluginApi.patch || !PluginApi.patch.before || !Nav || !Tab) {
      console.warn(logPrefix, 'Required Stash PluginApi parts are missing');
      return;
    }

    const h = React.createElement;
    const useEffect = React.useEffect;
    const useMemo = React.useMemo;
    const useState = React.useState;

    function isRenderableReactNode(value) {
      if (value == null || typeof value === 'boolean') return true;
      if (typeof value === 'string' || typeof value === 'number') return true;
      if (Array.isArray(value)) return value.every(isRenderableReactNode);
      if (React.isValidElement && React.isValidElement(value)) return true;
      return !!(value && value.$$typeof === Symbol.for('react.element'));
    }

    function patchResultOrNull(result) {
      return isRenderableReactNode(result) ? result : null;
    }

    function usePersistentSetting(key, fallback) {
      const initial = Core.readSetting(key, fallback);
      const pair = useState(initial);
      const value = pair[0];
      const setValue = pair[1];
      useEffect(function () {
        Core.writeSetting(key, value);
      }, [key, value]);
      return [value, setValue];
    }

    function usePersistentBooleanSetting(key, fallback) {
      const initial = Core.readSetting(key, fallback ? 'true' : 'false') === 'true';
      const pair = useState(initial);
      const value = pair[0];
      const setValue = pair[1];
      useEffect(function () {
        Core.writeSetting(key, value ? 'true' : 'false');
      }, [key, value]);
      return [value, setValue];
    }

    function graphQLRequest(query, variables) {
      const endpoint = new URL('graphql', document.baseURI || window.location.href).toString();
      return fetch(endpoint, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ query: query, variables: variables || {} }),
      })
        .then(function (res) {
          if (!res.ok) throw new Error('Stash GraphQL HTTP ' + res.status);
          return res.json();
        })
        .then(function (payload) {
          const error = Core.extractGraphQLErrorMessage(payload);
          if (error) throw new Error(error);
          return payload.data || {};
        });
    }

    function getPluginById(pluginId) {
      return graphQLRequest(Core.buildPluginsQuery()).then(function (data) {
        const plugins = Array.isArray(data.plugins) ? data.plugins : [];
        return plugins.find(function (plugin) { return plugin && plugin.id === pluginId; }) || null;
      });
    }

    function runEngineTaskById(enginePluginId, taskName, args) {
      return graphQLRequest(
        Core.buildRunPluginTaskMutation(),
        Core.buildRunPluginTaskVariables(enginePluginId, taskName, args)
      );
    }

    function readEngineStatus(enginePluginId) {
      return graphQLRequest(Core.buildRunPluginOperationMutation(), { plugin_id: enginePluginId, args: { mode: 'status' } })
        .then(function (data) { return Core.parsePluginOperationOutput(data.runPluginOperation); });
    }

    function readStashSceneCount() {
      return graphQLRequest(Core.buildSceneCountQuery()).then(function (data) {
        const count = data && data.findScenes ? Number(data.findScenes.count) : 0;
        return Number.isFinite(count) && count >= 0 ? count : 0;
      });
    }

    function parseCount(value) {
      const count = Number(value);
      return Number.isFinite(count) && count >= 0 ? count : 0;
    }

    function triggerAutoSync(enginePluginId, options) {
      const opts = options || {};
      return getPluginById(enginePluginId).then(function (plugin) {
        if (!plugin) throw new Error('Engine plugin is not loaded. Install it, then reload plugins.');
        if (!plugin.enabled) throw new Error('Engine plugin is disabled. Enable it first.');
        return Promise.all([readStashSceneCount(), readEngineStatus(enginePluginId)]).then(function (results) {
          const stashSceneCount = results[0];
          const status = results[1] || {};
          const dbSceneCount = parseCount(status.sceneCount);
          const lastTriggered = parseCount(Core.readSetting('autoSyncLastTriggeredSceneCount', '0'));
          const lastPruneTriggered = parseCount(Core.readSetting('autoSyncLastTriggeredPruneSceneCount', '-1'));
          if (opts.onStatus) opts.onStatus(status, plugin);
          if (stashSceneCount > dbSceneCount && stashSceneCount > lastTriggered) {
            Core.writeSetting('autoSyncLastTriggeredSceneCount', String(stashSceneCount));
            return runEngineTaskById(enginePluginId, 'Bootstrap recommendations', Core.buildBootstrapTaskArgs()).then(function (data) {
              return { started: true, action: 'index', data: data, stashSceneCount: stashSceneCount, dbSceneCount: dbSceneCount };
            });
          }
          if (dbSceneCount > stashSceneCount && stashSceneCount !== lastPruneTriggered) {
            Core.writeSetting('autoSyncLastTriggeredPruneSceneCount', String(stashSceneCount));
            return runEngineTaskById(enginePluginId, 'Prune deleted scenes', Core.buildPruneDeletedScenesTaskArgs()).then(function (data) {
              return { started: true, action: 'prune', data: data, stashSceneCount: stashSceneCount, dbSceneCount: dbSceneCount };
            });
          }
          return { started: false, stashSceneCount: stashSceneCount, dbSceneCount: dbSceneCount };
        });
      });
    }

    function OnboardingPanel(props) {
      if (!props || props.pluginID !== PLUGIN_ID) return null;

      const inferred = Core.inferEngineTarget({
        userAgent: window.navigator && window.navigator.userAgent,
        platform: window.navigator && window.navigator.platform,
      });
      const targetPair = usePersistentSetting('engineTarget', Core.DEFAULT_ENGINE_TARGET);
      const engineTarget = targetPair[0];
      const setEngineTarget = targetPair[1];
      const packageSourcePair = usePersistentSetting('enginePackageSource', Core.DEFAULT_ENGINE_PACKAGE_SOURCE);
      const packageSource = packageSourcePair[0];
      const setPackageSource = packageSourcePair[1];
      const importDbPathPair = usePersistentSetting('importDbPath', '');
      const importDbPath = importDbPathPair[0];
      const setImportDbPath = importDbPathPair[1];
      const autoSyncPair = usePersistentBooleanSetting('autoSync', false);
      const autoSync = autoSyncPair[0];
      const setAutoSync = autoSyncPair[1];
      const statePair = useState({ busy: '', error: '', message: '', details: null });
      const state = statePair[0];
      const setState = statePair[1];
      const dbStatusPair = useState({ loading: false, error: '', data: null, plugin: null });
      const dbStatus = dbStatusPair[0];
      const setDbStatus = dbStatusPair[1];
      const autoStatePair = useState({ checking: false, error: '', message: '' });
      const autoState = autoStatePair[0];
      const setAutoState = autoStatePair[1];
      const selectedEngine = Core.getEngineTarget(engineTarget);
      const packageSpec = Core.buildEnginePackageSpec(selectedEngine.id, packageSource);

      function formatCount(value) {
        const n = Number(value);
        return Number.isFinite(n) ? n.toLocaleString() : '0';
      }

      function formatBytes(value) {
        const bytes = Number(value);
        if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
        const units = ['B', 'KB', 'MB', 'GB'];
        let size = bytes;
        let unit = 0;
        while (size >= 1024 && unit < units.length - 1) {
          size /= 1024;
          unit += 1;
        }
        return (unit === 0 ? String(Math.round(size)) : size.toFixed(size >= 10 ? 1 : 2)) + ' ' + units[unit];
      }

      function formatDate(value) {
        if (!value) return '-';
        const parsed = new Date(String(value));
        if (Number.isNaN(parsed.getTime())) return String(value);
        return parsed.toLocaleString();
      }

      function runAction(label, fn) {
        setState({ busy: label, error: '', message: '', details: null });
        Promise.resolve()
          .then(fn)
          .then(function (result) {
            setState({ busy: '', error: '', message: result && result.message ? result.message : label + ' complete', details: result && result.details ? result.details : null });
          })
          .catch(function (err) {
            setState({ busy: '', error: err && err.message ? err.message : String(err), message: '', details: null });
          });
      }

      function validatePackageSource() {
        const source = Core.normalizePackageSourceUrl(packageSource);
        const parsed = new URL(source, window.location.href);
        if (parsed.protocol !== 'file:' && parsed.protocol !== 'https:' && parsed.hostname !== window.location.hostname && parsed.hostname !== '127.0.0.1' && parsed.hostname !== 'localhost') {
          throw new Error('Package source URL must be file://, https://, current Stash host, localhost, or 127.0.0.1.');
        }
      }

      function getEnginePluginStatus() {
        return getPluginById(selectedEngine.enginePluginId);
      }

      function requireEnginePlugin(options) {
        return getEnginePluginStatus().then(function (plugin) {
          if (!plugin) {
            throw new Error('Engine plugin is not loaded. Install it, then reload plugins.');
          }
          if (options && options.enabled && !plugin.enabled) {
            throw new Error('Engine plugin is disabled. Enable it first.');
          }
          return plugin;
        });
      }

      function runEngineTask(taskName, args) {
        return runEngineTaskById(selectedEngine.enginePluginId, taskName, args);
      }

      function refreshDbStatus() {
        setDbStatus({ loading: true, error: '', data: dbStatus.data, plugin: dbStatus.plugin });
        return getEnginePluginStatus().then(function (plugin) {
          if (!plugin) {
            setDbStatus({ loading: false, error: 'Engine plugin is not loaded.', data: null, plugin: null });
            return null;
          }
          if (!plugin.enabled) {
            setDbStatus({ loading: false, error: '', data: null, plugin: plugin });
            return null;
          }
          return graphQLRequest(Core.buildRunPluginOperationMutation(), { plugin_id: selectedEngine.enginePluginId, args: { mode: 'status' } })
            .then(function (data) {
              setDbStatus({ loading: false, error: '', data: Core.parsePluginOperationOutput(data.runPluginOperation), plugin: plugin });
            });
        }).catch(function (err) {
          setDbStatus({ loading: false, error: err && err.message ? err.message : String(err), data: null, plugin: dbStatus.plugin });
        });
      }

      function metric(label, value) {
        return h('div', { className: 'stash-hybrid-db-status__metric' },
          h('span', null, label),
          h('strong', null, value)
        );
      }

      function renderDbStatusBody() {
        if (dbStatus.loading && !dbStatus.data) {
          return h('div', { className: 'stash-hybrid-db-status__hint' }, 'Loading database status…');
        }
        if (dbStatus.error) {
          return h(Alert, { variant: 'warning', className: 'stash-hybrid-onboarding__alert' }, dbStatus.error);
        }
        if (dbStatus.plugin && !dbStatus.plugin.enabled) {
          return h('div', { className: 'stash-hybrid-db-status__hint' }, 'Engine plugin is disabled.');
        }
        if (!dbStatus.data) {
          return h('div', { className: 'stash-hybrid-db-status__hint' }, 'Engine status not loaded.');
        }
        const data = dbStatus.data || {};
        const database = data.database && typeof data.database === 'object' ? data.database : {};
        const caches = Array.isArray(data.recommendationCaches) ? data.recommendationCaches : [];
        const exists = database.exists === true;
        return h(React.Fragment, null,
          h('div', { className: 'stash-hybrid-db-status__metrics' },
            metric('DB', exists ? 'Present' : 'Missing'),
            metric('Schema', formatCount(database.schemaVersion)),
            metric('Scenes', formatCount(data.sceneCount)),
            metric('Size', formatBytes(database.sizeBytes)),
            metric('Default model', data.defaultModelVersion || Core.DEFAULT_INTEGRATED_MODEL_VERSION)
          ),
          database.updatedAt && h('div', { className: 'stash-hybrid-db-status__hint' }, 'Updated: ', formatDate(database.updatedAt)),
          caches.length > 0 ?
            h('div', { className: 'stash-hybrid-db-status__caches' },
              caches.map(function (cache) {
                return h('div', { key: cache.modelVersion, className: 'stash-hybrid-db-status__cache' },
                  h('strong', null, cache.modelVersion || 'unknown'),
                  h('span', null, formatCount(cache.sources), ' sources · ', formatCount(cache.rows), ' rows'),
                  cache.updatedAt && h('span', null, formatDate(cache.updatedAt))
                );
              })
            ) :
            h('div', { className: 'stash-hybrid-db-status__hint' }, exists ? 'No recommendation cache.' : 'No database file.')
        );
      }

      function maybeAutoSync() {
        if (!autoSync) return Promise.resolve(null);
        setAutoState({ checking: true, error: '', message: autoState.message || '' });
        return triggerAutoSync(selectedEngine.enginePluginId, {
          onStatus: function (status, plugin) {
            setDbStatus({ loading: false, error: '', data: status, plugin: plugin || dbStatus.plugin });
          },
        }).then(function (result) {
          if (result && result.started) {
            setAutoState({
              checking: false,
              error: '',
              message: 'Auto sync started. Job ID: ' + result.data.runPluginTask + ' · Stash scenes ' + formatCount(result.stashSceneCount) + ' / DB scenes ' + formatCount(result.dbSceneCount),
            });
            return result;
          }
          setAutoState({
            checking: false,
            error: '',
            message: 'Auto sync idle · Stash scenes ' + formatCount(result.stashSceneCount) + ' / DB scenes ' + formatCount(result.dbSceneCount),
          });
          return result;
        }).catch(function (err) {
          setAutoState({ checking: false, error: err && err.message ? err.message : String(err), message: '' });
          return null;
        });
      }

      useEffect(function () {
        refreshDbStatus();
      }, [selectedEngine.enginePluginId]);

      useEffect(function () {
        if (!autoSync) {
          setAutoState({ checking: false, error: '', message: '' });
          return undefined;
        }
        maybeAutoSync();
        if (!window.setInterval || !window.clearInterval) return undefined;
        const timer = window.setInterval(maybeAutoSync, 5 * 60 * 1000);
        return function () { window.clearInterval(timer); };
      }, [autoSync, selectedEngine.enginePluginId]);

      function installEngine() {
        runAction('Install engine', function () {
          validatePackageSource();
          return graphQLRequest(Core.buildInstallPackagesMutation(), { packages: [packageSpec] }).then(function (data) {
            return { message: 'Engine install started. Job ID: ' + data.installPackages, details: data };
          });
        });
      }

      function reloadPlugins() {
        runAction('Reload plugins', function () {
          return graphQLRequest(Core.buildReloadPluginsMutation()).then(function (data) {
            return { message: 'Plugins reloaded.', details: data };
          });
        });
      }

      function enableEngine() {
        runAction('Enable engine', function () {
          return requireEnginePlugin().then(function () {
            const enabledMap = {};
            enabledMap[selectedEngine.enginePluginId] = true;
            return graphQLRequest(Core.buildSetPluginsEnabledMutation(), { enabledMap: enabledMap });
          }).then(function (data) {
            return { message: selectedEngine.enginePluginId + ' enable requested.', details: data };
          });
        });
      }

      function preflightEngine() {
        runAction('Check engine', function () {
          return requireEnginePlugin({ enabled: true }).then(function () {
            return graphQLRequest(Core.buildRunPluginOperationMutation(), { plugin_id: selectedEngine.enginePluginId, args: { mode: 'status' } });
          }).then(function (data) {
            return { message: 'Engine status checked.', details: data };
          });
        });
      }

      function bootstrapDb() {
        runAction('Build database', function () {
          return requireEnginePlugin({ enabled: true }).then(function () {
            return runEngineTask('Bootstrap recommendations', Core.buildBootstrapTaskArgs());
          }).then(function (data) {
            return { message: 'Database build started. Job ID: ' + data.runPluginTask, details: data };
          });
        });
      }

      function indexScenesDb() {
        runAction('Index scenes', function () {
          return requireEnginePlugin({ enabled: true }).then(function () {
            return runEngineTask('Index scene metadata', Core.buildIndexScenesTaskArgs());
          }).then(function (data) {
            return { message: 'Scene indexing started. Job ID: ' + data.runPluginTask, details: data };
          });
        });
      }

      function rebuildCacheDb() {
        runAction('Rebuild cache', function () {
          return requireEnginePlugin({ enabled: true }).then(function () {
            return runEngineTask('Rebuild recommendation cache', Core.buildCacheTaskArgs());
          }).then(function (data) {
            return { message: 'Recommendation cache rebuild started. Job ID: ' + data.runPluginTask, details: data };
          });
        });
      }

      function pruneDeletedScenesDb() {
        runAction('Prune deleted scenes', function () {
          return requireEnginePlugin({ enabled: true }).then(function () {
            return runEngineTask('Prune deleted scenes', Core.buildPruneDeletedScenesTaskArgs());
          }).then(function (data) {
            return { message: 'Deleted-scene prune started. Job ID: ' + data.runPluginTask, details: data };
          });
        });
      }

      function importDbFile() {
        runAction('Import DB file', function () {
          const sourcePath = String(importDbPath || '').trim();
          if (!sourcePath) {
            throw new Error('Enter a DB file path that the engine can read.');
          }
          return requireEnginePlugin({ enabled: true }).then(function () {
            return runEngineTask('Import database file', Core.buildImportDbTaskArgs(sourcePath));
          }).then(function (data) {
            return { message: 'Database import started. Job ID: ' + data.runPluginTask, details: data };
          });
        });
      }

      function buildDevTest100Db() {
        runAction('Build dev 100-scene DB', function () {
          return requireEnginePlugin({ enabled: true }).then(function () {
            return graphQLRequest(Core.buildRunPluginTaskMutation(), Core.buildRunPluginTaskVariables(selectedEngine.enginePluginId, 'Build dev test DB (100 scenes)', Core.buildDevTest100TaskArgs()));
          }).then(function (data) {
            return { message: 'Dev 100-scene DB build started. Job ID: ' + data.runPluginTask + '. Check Settings → Tasks logs for verification scenes.', details: data };
          });
        });
      }

      return h('div', { className: 'stash-hybrid-onboarding' },
        h('div', { className: 'stash-hybrid-onboarding__head' },
          h('div', null,
            h('h5', null, 'Hybrid Recommendations'),
            null
          ),
          null
        ),
        h('div', { className: 'stash-hybrid-onboarding__grid' },
          h('label', null, 'Engine target',
            h('select', { className: 'form-control', value: selectedEngine.id, onChange: function (e) { setEngineTarget(e.target.value); } },
              Core.ENGINE_TARGETS.map(function (target) {
                return h('option', { key: target.id, value: target.id }, target.label + ' · ' + target.id);
              })
            ),
            h('small', null, 'Detected: ', h('code', null, inferred), ' · Docker: choose the container OS/arch.')
          ),
          h('label', null, 'Engine source URL',
            h('div', { className: 'stash-hybrid-onboarding__source-row' },
              h('input', { className: 'form-control', value: packageSource, placeholder: Core.DEFAULT_ENGINE_PACKAGE_SOURCE, onChange: function (e) { setPackageSource(e.target.value); } })
            ),
            null
          )
        ),
        h('div', { className: 'stash-hybrid-onboarding__summary' },
          h('div', null, 'Package ID: ', h('code', null, packageSpec.id)),
          h('div', null, 'Engine Plugin ID: ', h('code', null, selectedEngine.enginePluginId))
        ),
        h('div', { className: 'stash-hybrid-db-status' },
          h('div', { className: 'stash-hybrid-db-status__head' },
            h('div', null,
              h('h6', null, 'Database'),
              h('p', null, selectedEngine.enginePluginId)
            ),
            h(Button, { size: 'sm', variant: 'secondary', disabled: dbStatus.loading, onClick: refreshDbStatus }, dbStatus.loading ? 'Loading…' : 'Refresh')
          ),
          renderDbStatusBody()
        ),
        h('div', { className: 'stash-hybrid-onboarding__steps' },
          h(Button, { size: 'sm', variant: 'primary', disabled: !!state.busy, onClick: installEngine }, state.busy === 'Install engine' ? 'Installing…' : 'Install engine'),
          h(Button, { size: 'sm', variant: 'secondary', disabled: !!state.busy, onClick: reloadPlugins }, state.busy === 'Reload plugins' ? 'Reloading…' : 'Reload plugins'),
          h(Button, { size: 'sm', variant: 'secondary', disabled: !!state.busy, onClick: enableEngine }, state.busy === 'Enable engine' ? 'Enabling…' : 'Enable engine'),
          h(Button, { size: 'sm', variant: 'secondary', disabled: !!state.busy, onClick: preflightEngine }, state.busy === 'Check engine' ? 'Checking…' : 'Preflight'),
          h(Button, { size: 'sm', variant: 'success', disabled: !!state.busy, onClick: bootstrapDb }, state.busy === 'Build database' ? 'Starting…' : 'Build DB'),
          h(Button, { size: 'sm', variant: 'secondary', disabled: !!state.busy, onClick: indexScenesDb }, state.busy === 'Index scenes' ? 'Starting…' : 'Index scenes'),
          h(Button, { size: 'sm', variant: 'secondary', disabled: !!state.busy, onClick: rebuildCacheDb }, state.busy === 'Rebuild cache' ? 'Starting…' : 'Rebuild cache'),
          h(Button, { size: 'sm', variant: 'secondary', disabled: !!state.busy, onClick: pruneDeletedScenesDb }, state.busy === 'Prune deleted scenes' ? 'Starting…' : 'Prune deleted scenes')
        ),
        h('div', { className: 'stash-hybrid-onboarding__automation' },
          h('label', { className: 'stash-hybrid-onboarding__toggle' },
            h('input', { type: 'checkbox', checked: autoSync, onChange: function (e) { setAutoSync(Boolean(e.target.checked)); }, 'aria-label': 'Auto sync Stash scene additions and deletions' }),
            h('span', null, 'Auto sync Stash scene additions/deletions')
          ),
          autoState.checking && h('span', { className: 'stash-hybrid-onboarding__auto-state' }, 'Checking…'),
          autoState.message && h('span', { className: 'stash-hybrid-onboarding__auto-state' }, autoState.message),
          autoState.error && h('span', { className: 'stash-hybrid-onboarding__auto-error' }, autoState.error)
        ),
        h('div', { className: 'stash-hybrid-onboarding__import' },
          h('label', null, 'Import existing DB file',
            h('div', { className: 'stash-hybrid-onboarding__import-row' },
              h('input', { className: 'form-control', value: importDbPath, placeholder: '/path/inside/stash/container/recommendations.sqlite', onChange: function (e) { setImportDbPath(e.target.value); }, 'aria-label': 'Existing recommendation DB file path' }),
              h(Button, { size: 'sm', variant: 'secondary', disabled: !!state.busy || !String(importDbPath || '').trim(), onClick: importDbFile }, state.busy === 'Import DB file' ? 'Starting…' : 'Import DB file')
            ),
            h('small', null, 'The engine backs up the current DB, imports this SQLite file, then migrates it if needed.')
          )
        ),
        h('div', { className: 'stash-hybrid-onboarding__dev' },
          h(Alert, { variant: 'warning', className: 'stash-hybrid-onboarding__alert' },
            h('strong', null, 'Development only: '),
            'Backs up the current engine DB, then builds a 100-scene test DB.'
          ),
          h(Button, { size: 'sm', variant: 'warning', disabled: !!state.busy, onClick: buildDevTest100Db }, state.busy === 'Build dev 100-scene DB' ? 'Starting…' : 'Build dev 100-scene DB')
        ),
        state.busy && h(Alert, { variant: 'info', className: 'stash-hybrid-onboarding__alert' }, state.busy + '…'),
        state.message && h(Alert, { variant: 'success', className: 'stash-hybrid-onboarding__alert' }, state.message, h('div', null, 'Check progress in Settings → Tasks.')),
        state.error && h(Alert, { variant: 'danger', className: 'stash-hybrid-onboarding__alert' }, h('strong', null, 'Error: '), state.error),
        state.details && h('pre', { className: 'stash-hybrid-onboarding__details' }, JSON.stringify(state.details, null, 2))
      );
    }

    function RecommendationsPanel(props) {
      const sceneId = props && props.sceneId ? String(props.sceneId) : '';
      const engineTargetPair = usePersistentSetting('engineTarget', Core.DEFAULT_ENGINE_TARGET);
      const engineTarget = engineTargetPair[0];
      const limitPair = usePersistentSetting('limit', String(Core.DEFAULT_LIMIT));
      const limit = limitPair[0];
      const setLimit = limitPair[1];

      const statePair = useState({ loading: false, error: '', warning: '', data: null });
      const state = statePair[0];
      const setState = statePair[1];

      const normalizedLimit = useMemo(function () {
        return Core.clampLimit(limit);
      }, [limit]);
      const selectedEngine = useMemo(function () {
        return Core.getEngineTarget(engineTarget);
      }, [engineTarget]);

      function loadFromIntegrated() {
        return getPluginById(selectedEngine.enginePluginId).then(function (plugin) {
          if (!plugin) throw new Error('Engine plugin ' + selectedEngine.enginePluginId + ' is not loaded. Install it, then reload plugins.');
          if (!plugin.enabled) throw new Error('Engine plugin ' + selectedEngine.enginePluginId + ' is disabled. Enable it first.');
          return graphQLRequest(
            Core.buildRunPluginOperationMutation(),
            Core.buildRecommendOperationVariables(selectedEngine.enginePluginId, sceneId, normalizedLimit)
          );
        }).then(function (data) {
          const normalized = Core.normalizeRecommendationResponse(data.runPluginOperation, {
            sourceSceneId: sceneId,
          });
          normalized.backendMode = 'integrated';
          return normalized;
        });
      }

      function load() {
        if (!sceneId) {
          setState({ loading: false, error: 'Scene ID not found.', warning: '', data: null });
          return;
        }
        setState({ loading: true, error: '', warning: '', data: state.data });
        loadFromIntegrated()
          .then(function (data) {
            setState({ loading: false, error: '', warning: data.warning || '', data: data });
          })
          .catch(function (err) {
            setState({ loading: false, error: err && err.message ? err.message : String(err), warning: '', data: null });
          });
      }

      useEffect(function () {
        load();
      }, [sceneId, normalizedLimit, selectedEngine.enginePluginId]);

      useEffect(function () {
        if (Core.readSetting('autoSync', 'false') !== 'true') return undefined;
        function sync() {
          triggerAutoSync(selectedEngine.enginePluginId).catch(function (err) {
            console.warn(logPrefix, 'Auto sync skipped:', err && err.message ? err.message : String(err));
          });
        }
        sync();
        if (!window.setInterval || !window.clearInterval) return undefined;
        const timer = window.setInterval(sync, 5 * 60 * 1000);
        return function () { window.clearInterval(timer); };
      }, [selectedEngine.enginePluginId]);

      const recommendations = state.data && Array.isArray(state.data.recommendations) ? state.data.recommendations : [];
      return h('div', { className: 'stash-hybrid-rec' },
        h('div', { className: 'stash-hybrid-rec__header' },
          h('div', null,
            h('h4', { className: 'stash-hybrid-rec__title' }, 'Hybrid Recommendations'),
            h('div', { className: 'stash-hybrid-rec__subtitle' },
              state.data ? ('Integrated engine · cacheHit=' + String(state.data.cacheHit) + (state.data.fallbackReason ? ' · ' + state.data.fallbackReason : '')) :
                'Integrated engine'
            )
          ),
          h('div', { className: 'stash-hybrid-rec__actions' },
            h('label', { className: 'stash-hybrid-rec__limit-control' },
              h('span', null, 'Limit'),
              h('input', { className: 'form-control', type: 'number', min: 1, max: 100, value: limit, onChange: function (e) { setLimit(e.target.value); }, 'aria-label': 'Hybrid recommendation limit' })
            ),
            h(Button, { variant: 'primary', size: 'sm', onClick: load, disabled: state.loading }, state.loading ? 'Loading…' : 'Refresh')
          )
        ),
        state.warning && h(Alert, { variant: 'warning', className: 'stash-hybrid-rec__alert' }, state.warning),
        state.error && h(Alert, { variant: 'danger', className: 'stash-hybrid-rec__alert' },
          h('strong', null, 'Integrated engine error: '), state.error,
          h('div', { className: 'stash-hybrid-rec__hint' }, 'Check Settings → Plugins → Stash Hybrid Recommendations.')
        ),
        state.loading && !recommendations.length && h('div', { className: 'stash-hybrid-rec__loading' }, h(Spinner, { animation: 'border', size: 'sm' }), ' Loading recommendations…'),
        !state.loading && !state.error && state.data && recommendations.length === 0 && h(Alert, { variant: 'warning', className: 'stash-hybrid-rec__alert' }, 'No recommendations. Check the database status.'),
        recommendations.length > 0 && h('div', { className: 'stash-hybrid-rec__grid' }, recommendations.map(function (rec, index) {
          return h(RecommendationCard, { key: rec.sceneId || index, recommendation: rec, index: index, Core: Core });
        }))
      );
    }

    function RecommendationCard(props) {
      const rec = props.recommendation || {};
      const scene = rec.scene || {};
      const imageUrl = props.Core.pickSceneImage(scene, window.location);
      const link = '/scenes/' + encodeURIComponent(scene.id || rec.sceneId || '');
      const title = scene.title || scene.fileName || scene.id || rec.sceneId || 'Untitled';
      const reasonText = Array.isArray(rec.reasons) ? rec.reasons.join(' · ') : '';
      const dimensions = scene.width && scene.height ? (scene.width + '×' + scene.height) : '';
      const meta = [scene.durationSeconds ? props.Core.formatDuration(scene.durationSeconds) : '', dimensions, scene.playCount != null ? ('Plays ' + scene.playCount) : ''].filter(Boolean).join(' · ');
      return h('a', { className: 'stash-hybrid-rec-card', href: link, title: title },
        h('div', { className: 'stash-hybrid-rec-card__imageWrap' },
          imageUrl ? h('img', { className: 'stash-hybrid-rec-card__image', src: imageUrl, loading: 'lazy', alt: '' }) : h('div', { className: 'stash-hybrid-rec-card__placeholder' }, 'No image'),
          h('div', { className: 'stash-hybrid-rec-card__score' }, props.Core.formatPercent(rec.score))
        ),
        h('div', { className: 'stash-hybrid-rec-card__body' },
          h('div', { className: 'stash-hybrid-rec-card__title' }, title),
          meta && h('div', { className: 'stash-hybrid-rec-card__meta' }, meta),
          reasonText && h('div', { className: 'stash-hybrid-rec-card__reasons' }, reasonText)
        )
      );
    }

    function findNestedEventKey(child) {
      try {
        if (!child || !child.props) return undefined;
        if (child.props.eventKey) return child.props.eventKey;
        if (child.props['data-event-key']) return child.props['data-event-key'];
        if (child.props['data-rb-event-key']) return child.props['data-rb-event-key'];
        const children = Array.isArray(child.props.children) ? child.props.children : [child.props.children];
        for (const c of children) {
          const nested = findNestedEventKey(c);
          if (nested) return nested;
        }
      } catch (_) {}
      return undefined;
    }

    function appendOrInsertAfterDetails(childrenArray, item) {
      const existing = childrenArray.findIndex(function (c) { return c && c.key === item.key; });
      if (existing >= 0) return childrenArray;
      const detailsIndex = childrenArray.findIndex(function (c) {
        const key = findNestedEventKey(c);
        return key === 'scene-details-panel' || (key && String(key).toLowerCase().includes('detail'));
      });
      const insertIndex = detailsIndex >= 0 ? detailsIndex + 1 : childrenArray.length;
      childrenArray.splice(insertIndex, 0, item);
      return childrenArray;
    }

    PluginApi.patch.before('ScenePage.Tabs', function (props) {
      const childrenArray = props && props.children ? (Array.isArray(props.children) ? props.children.slice() : [props.children]) : [];
      const navItem = h(Nav.Item, { key: 'stash-hybrid-rec-nav-item' },
        h(Nav.Link, { eventKey: TAB_KEY, key: 'stash-hybrid-rec-nav-link' }, 'Hybrid Recommendations')
      );
      return [{ children: h(React.Fragment, null, ...appendOrInsertAfterDetails(childrenArray, navItem)) }];
    });

    PluginApi.patch.before('ScenePage.TabContent', function (props) {
      const childrenArray = props && props.children ? (Array.isArray(props.children) ? props.children.slice() : [props.children]) : [];
      const sceneId = props && props.scene && props.scene.id ? String(props.scene.id) : '';
      const pane = h(Tab.Pane, { eventKey: TAB_KEY, key: 'stash-hybrid-rec-pane-' + (sceneId || 'loading') },
        sceneId ? h(RecommendationsPanel, { sceneId: sceneId }) : h('div', { className: 'stash-hybrid-rec' }, 'Waiting for scene data…')
      );
      return [{ children: h(React.Fragment, null, ...appendOrInsertAfterDetails(childrenArray, pane)) }];
    });

    if (PluginApi.patch.after) {
      PluginApi.patch.after('PluginSettings', function () {
        const args = Array.prototype.slice.call(arguments);
        const props = args[0];
        const result = args[args.length - 1];
        if (!props || props.pluginID !== PLUGIN_ID) return result;
        return h(React.Fragment, null, patchResultOrNull(result), h(OnboardingPanel, { pluginID: props.pluginID }));
      });
    }

    window.StashHybridRecommendations = { version: '0.3.0', pluginId: PLUGIN_ID };
    console.info(logPrefix, 'initialized');
  }

  function fallbackButton(React) {
    return function Button(props) {
      const p = Object.assign({}, props);
      delete p.variant;
      delete p.size;
      return React.createElement('button', p, props.children);
    };
  }
  function fallbackForm(React) { return { Control: function Control(props) { return React.createElement('input', props); } }; }
  function fallbackAlert(React) { return function Alert(props) { return React.createElement('div', props, props.children); }; }
  function fallbackSpinner(React) { return function Spinner() { return React.createElement('span', null, '…'); }; }

  waitForPluginApi(0);
})();
