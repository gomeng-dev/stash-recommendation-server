import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const elementSymbol = Symbol.for('react.element');
const beforePatches = new Map();
const afterPatches = new Map();

function makeElement(type, props, ...children) {
  return {
    $$typeof: elementSymbol,
    type,
    key: props && props.key ? props.key : null,
    props: Object.assign({}, props || {}, { children }),
  };
}

const React = {
  Fragment: 'Fragment',
  createElement: makeElement,
  isValidElement(value) {
    return !!value && value.$$typeof === elementSymbol;
  },
  useEffect() {},
  useMemo(fn) { return fn(); },
  useState(initial) { return [initial, function () {}]; },
};

const context = {
  console,
  URL,
  URLSearchParams,
  fetch() { throw new Error('fetch should not run in render safety test'); },
  document: { baseURI: 'http://127.0.0.1:9999/' },
  navigator: { userAgent: 'Mozilla/5.0', platform: 'Linux aarch64' },
  localStorage: { getItem() { return null; }, setItem() {} },
  window: {},
  globalThis: {},
};
context.window = context;
context.globalThis = context;
context.PluginApi = {
  React,
  libraries: {
    Bootstrap: {
      Nav: { Item: 'Nav.Item', Link: 'Nav.Link' },
      Tab: { Pane: 'Tab.Pane' },
      Button: 'Button',
      Alert: 'Alert',
      Spinner: 'Spinner',
    },
  },
  patch: {
    before(name, fn) { beforePatches.set(name, fn); },
    after(name, fn) { afterPatches.set(name, fn); },
  },
};

vm.createContext(context);
vm.runInContext(readFileSync(new URL('../stashHybridRecommendationsCore.js', import.meta.url), 'utf8'), context);
vm.runInContext(readFileSync(new URL('../stashHybridRecommendations.js', import.meta.url), 'utf8'), context);

const pluginSettingsPatch = afterPatches.get('PluginSettings');
assert.equal(typeof pluginSettingsPatch, 'function', 'PluginSettings after patch should be registered');

function containsReference(node, ref) {
  if (node === ref) return true;
  if (!node || typeof node !== 'object') return false;
  if (Array.isArray(node)) return node.some((child) => containsReference(child, ref));
  const children = node.props && node.props.children;
  return containsReference(children, ref);
}

function containsPlainObjectChild(node) {
  if (node == null || typeof node === 'boolean' || typeof node === 'string' || typeof node === 'number') return false;
  if (Array.isArray(node)) return node.some(containsPlainObjectChild);
  if (node && node.$$typeof === elementSymbol) {
    return containsPlainObjectChild(node.props && node.props.children);
  }
  return true;
}

function renderFunctionComponents(node) {
  if (node == null || typeof node === 'boolean' || typeof node === 'string' || typeof node === 'number') return node;
  if (Array.isArray(node)) return node.map(renderFunctionComponents);
  if (node && node.$$typeof === elementSymbol) {
    if (typeof node.type === 'function') {
      return renderFunctionComponents(node.type(Object.assign({}, node.props || {}, { children: node.props && node.props.children })));
    }
    return Object.assign({}, node, {
      props: Object.assign({}, node.props || {}, { children: renderFunctionComponents(node.props && node.props.children) }),
    });
  }
  return node;
}

function collectText(node) {
  if (node == null || typeof node === 'boolean') return '';
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(collectText).join(' ');
  if (node && node.$$typeof === elementSymbol) return collectText(node.props && node.props.children);
  return '';
}

function findElement(node, predicate) {
  if (node == null || typeof node !== 'object') return null;
  if (Array.isArray(node)) {
    for (const child of node) {
      const found = findElement(child, predicate);
      if (found) return found;
    }
    return null;
  }
  if (node.$$typeof === elementSymbol) {
    if (predicate(node)) return node;
    return findElement(node.props && node.props.children, predicate);
  }
  return null;
}

const patchContext = {};
const originalResult = React.createElement('div', { className: 'plugin-settings' }, 'original settings');
const nonTargetResult = pluginSettingsPatch({ pluginID: 'SomeOtherPlugin' }, patchContext, originalResult);
assert.equal(
  nonTargetResult,
  originalResult,
  'PluginSettings patch must return the real original result for non-target plugins'
);

const nonRenderableOriginalResult = {};
const patched = pluginSettingsPatch({ pluginID: 'StashHybridRecommendations' }, patchContext, nonRenderableOriginalResult);
assert.equal(
  containsReference(patched, nonRenderableOriginalResult),
  false,
  'PluginSettings patch must not pass a plain object original result through as a React child'
);
assert.equal(
  containsPlainObjectChild(patched),
  false,
  'PluginSettings patch must not include plain object children'
);
const renderedSettings = renderFunctionComponents(patched);
const settingsText = collectText(renderedSettings);
assert.match(settingsText, /Database/, 'PluginSettings should expose current DB status');
assert.match(settingsText, /Refresh/, 'PluginSettings DB status should expose a refresh action');
assert.match(settingsText, /Index scenes/, 'PluginSettings should expose separate scene indexing');
assert.match(settingsText, /Rebuild cache/, 'PluginSettings should expose separate cache rebuild');
assert.match(settingsText, /Import existing DB file/, 'PluginSettings should expose DB file import');
assert.match(settingsText, /Prune deleted scenes/, 'PluginSettings should expose deleted-scene pruning');
assert.match(settingsText, /Auto sync Stash scene additions\/deletions/, 'PluginSettings should expose automatic scene sync toggle');
assert.match(settingsText, /Build dev 100-scene DB/, 'PluginSettings should expose a development-only 100 scene DB action');
assert.doesNotMatch(settingsText, /Preset/, 'PluginSettings should not expose the legacy package source preset selector');
assert.doesNotMatch(settingsText, /MVP|onboarding MVP|automatic setup/i, 'settings UI should avoid verbose onboarding copy');

const tabContentPatch = beforePatches.get('ScenePage.TabContent');
assert.equal(typeof tabContentPatch, 'function', 'ScenePage.TabContent before patch should be registered');
const tabPatchResult = tabContentPatch({
  scene: { id: 'scene-1' },
  children: React.createElement('div', { key: 'details' }, 'Details'),
});
const renderedTab = renderFunctionComponents(tabPatchResult[0].children);
const renderedText = collectText(renderedTab);
assert.match(renderedText, /Limit/, 'recommendation tab should expose output count input');
assert.doesNotMatch(renderedText, /Backend mode|Recommendation API URL|Engine target|Close settings/, 'recommendation tab should not expose backend, API, or engine target settings');
const limitInput = findElement(renderedTab, (node) => node.type === 'input' && node.props && node.props['aria-label'] === 'Hybrid recommendation limit');
assert.ok(limitInput, 'recommendation tab should render the output count number input');
assert.equal(limitInput.props.type, 'number');
assert.equal(limitInput.props.value, '15');

console.log('ui render safety tests passed');
