#!/usr/bin/env python3
"""Validate Stash Hybrid Recommendations package-source assets."""

from __future__ import annotations

import hashlib
import zipfile
from pathlib import Path

try:
    import yaml
except ModuleNotFoundError:  # pragma: no cover - exercised in dependency-free environments.
    yaml = None

REPO_ROOT = Path(__file__).resolve().parents[1]
PACKAGE_SOURCE_DIR = REPO_ROOT / "plugins" / "StashHybridRecommendations" / "package-source"
PUBLIC_INDEX = PACKAGE_SOURCE_DIR / "index.yml"
ENGINE_INDEX = PACKAGE_SOURCE_DIR / "engines.yml"
PUBLIC_STASH_SOURCE = PACKAGE_SOURCE_DIR / "index"
ENGINE_STASH_SOURCE = PACKAGE_SOURCE_DIR / "engines"
REQUIRED_FIELDS = {"id", "name", "version", "date", "path", "sha256"}


def parse_scalar(value: str) -> object:
    text = value.strip()
    if text == "true":
        return True
    if text == "false":
        return False
    if len(text) >= 2 and text[0] == "'" and text[-1] == "'":
        return text[1:-1].replace("''", "'")
    return text


def split_key_value(text: str) -> tuple[str, str]:
    key, sep, value = text.partition(":")
    if not sep:
        raise AssertionError(f"unsupported YAML line: {text!r}")
    return key.strip(), value.strip()


def fallback_load_yaml(text: str) -> object:
    lines = [line.rstrip() for line in text.splitlines() if line.strip() and not line.lstrip().startswith("#")]
    if not lines:
        return None
    if lines[0].startswith("- "):
        items: list[dict[str, object]] = []
        current: dict[str, object] | None = None
        nested_key: str | None = None
        for line in lines:
            indent = len(line) - len(line.lstrip(" "))
            stripped = line.strip()
            if indent == 0 and stripped.startswith("- "):
                current = {}
                items.append(current)
                nested_key = None
                rest = stripped[2:].strip()
                if rest:
                    key, value = split_key_value(rest)
                    current[key] = parse_scalar(value)
                continue
            if current is None:
                raise AssertionError(f"YAML list item continuation without item: {line!r}")
            if indent == 2:
                key, value = split_key_value(stripped)
                if value:
                    current[key] = parse_scalar(value)
                    nested_key = None
                else:
                    current[key] = {}
                    nested_key = key
                continue
            if indent == 4 and nested_key:
                key, value = split_key_value(stripped)
                nested = current.get(nested_key)
                if not isinstance(nested, dict):
                    raise AssertionError(f"YAML nested target is not a mapping: {line!r}")
                nested[key] = parse_scalar(value)
                continue
            raise AssertionError(f"unsupported YAML index line: {line!r}")
        return items

    result: dict[str, object] = {}
    current_key: str | None = None
    for line in lines:
        indent = len(line) - len(line.lstrip(" "))
        stripped = line.strip()
        if indent == 0:
            key, value = split_key_value(stripped)
            if value:
                result[key] = parse_scalar(value)
                current_key = None
            else:
                result[key] = []
                current_key = key
            continue
        if indent == 2 and current_key and stripped.startswith("- "):
            target = result.get(current_key)
            if not isinstance(target, list):
                raise AssertionError(f"YAML nested target is not a list: {line!r}")
            item = stripped[2:].strip()
            if ":" in item:
                key, value = split_key_value(item)
                target.append({key: parse_scalar(value)})
            else:
                target.append(parse_scalar(item))
            continue
    return result


def load_yaml_text(text: str) -> object:
    if yaml is not None:
        return yaml.safe_load(text)
    return fallback_load_yaml(text)


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def load_yaml_from_zip(zf: zipfile.ZipFile, names: list[str]) -> dict:
    yml_names = [name for name in names if name.endswith(".yml")]
    if len(yml_names) != 1:
        raise AssertionError(f"expected exactly one plugin yml, found {yml_names}")
    with zf.open(yml_names[0]) as f:
        data = load_yaml_text(f.read().decode("utf-8"))
    if not isinstance(data, dict):
        raise AssertionError(f"{yml_names[0]} did not parse as a YAML mapping")
    return data


def load_index(path: Path) -> list[dict]:
    if not path.exists():
        raise SystemExit(f"missing package index: {path}")
    items = load_yaml_text(path.read_text(encoding="utf-8"))
    if not isinstance(items, list) or not items:
        raise SystemExit(f"package index must be a non-empty YAML list: {path}")
    return items


def validate_source_alias(canonical: Path, alias: Path) -> None:
    if not alias.exists():
        raise AssertionError(f"missing extensionless Stash package source alias: {alias}")
    if canonical.read_text(encoding="utf-8") != alias.read_text(encoding="utf-8"):
        raise AssertionError(f"extensionless Stash package source alias differs from canonical index: {alias}")
    if alias.suffix in {".yml", ".yaml"}:
        raise AssertionError(f"Stash package source alias must not end with .yml/.yaml: {alias}")


def validate_items(index_path: Path, expected_role: str) -> tuple[int, int]:
    items = load_index(index_path)
    ids: set[str] = set()
    engine_count = 0
    onboarding_count = 0
    for item in items:
        if not isinstance(item, dict):
            raise AssertionError(f"{index_path} item is not a mapping: {item!r}")
        missing = REQUIRED_FIELDS - set(item)
        if missing:
            raise AssertionError(f"{item.get('id', '<unknown>')} missing fields: {sorted(missing)}")
        if item["id"] in ids:
            raise AssertionError(f"duplicate package id in {index_path}: {item['id']}")
        ids.add(str(item["id"]))

        zip_path = PACKAGE_SOURCE_DIR / str(item["path"])
        if not zip_path.exists():
            raise AssertionError(f"missing zip for {item['id']}: {zip_path}")
        actual_hash = sha256(zip_path)
        if actual_hash != item["sha256"]:
            raise AssertionError(f"sha256 mismatch for {zip_path}: {actual_hash} != {item['sha256']}")

        with zipfile.ZipFile(zip_path) as zf:
            bad = zf.testzip()
            if bad:
                raise AssertionError(f"corrupt member in {zip_path}: {bad}")
            names = zf.namelist()
            plugin_yaml = load_yaml_from_zip(zf, names)
            metadata = item.get("metadata") or {}
            role = metadata.get("packageRole")
            if role != expected_role:
                raise AssertionError(f"{item['id']} in {index_path.name} has role {role!r}, expected {expected_role!r}")
            if role == "engine":
                engine_count += 1
                engine_plugin_id = metadata.get("enginePluginId")
                if engine_plugin_id and f"{engine_plugin_id}.yml" not in names:
                    raise AssertionError(f"{zip_path} missing expected engine yml {engine_plugin_id}.yml")
                exec_list = plugin_yaml.get("exec")
                if not isinstance(exec_list, list) or not exec_list:
                    raise AssertionError(f"{zip_path} engine YAML must declare exec")
                executable = str(exec_list[0])
                if executable not in names:
                    raise AssertionError(f"{zip_path} missing executable {executable}")
                info = zf.getinfo(executable)
                mode = (info.external_attr >> 16) & 0o777
                if not executable.endswith(".exe") and not (mode & 0o111):
                    raise AssertionError(f"{zip_path} executable bit not set for {executable}")
            elif role == "onboarding-ui":
                onboarding_count += 1
                for required in [
                    "StashHybridRecommendations.yml",
                    "stashHybridRecommendationsCore.js",
                    "stashHybridRecommendations.js",
                    "stashHybridRecommendations.css",
                ]:
                    if required not in names:
                        raise AssertionError(f"{zip_path} missing {required}")
            else:
                raise AssertionError(f"{item['id']} missing/unknown metadata.packageRole")
    return onboarding_count, engine_count


def main() -> None:
    validate_source_alias(PUBLIC_INDEX, PUBLIC_STASH_SOURCE)
    validate_source_alias(ENGINE_INDEX, ENGINE_STASH_SOURCE)
    public_onboarding_count, public_engine_count = validate_items(PUBLIC_INDEX, "onboarding-ui")
    engine_onboarding_count, engine_engine_count = validate_items(ENGINE_INDEX, "engine")
    if public_onboarding_count != 1 or public_engine_count != 0:
        raise AssertionError(
            f"public index should expose exactly one onboarding package and no engines; "
            f"got onboarding={public_onboarding_count} engines={public_engine_count}"
        )
    if engine_onboarding_count != 0 or engine_engine_count < 1:
        raise AssertionError(
            f"engine index should expose only engine packages; "
            f"got onboarding={engine_onboarding_count} engines={engine_engine_count}"
        )
    print(
        f"package validation ok: public={public_onboarding_count} onboarding, "
        f"engines={engine_engine_count} hidden engine packages"
    )


if __name__ == "__main__":
    main()
