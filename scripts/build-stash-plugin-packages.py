#!/usr/bin/env python3
"""Build Stash Hybrid Recommendations plugin package zips and source index.

This script intentionally builds release assets locally without publishing them.
It keeps the development `file://` package source usable while producing the
same `index.yml` shape expected by Stash's plugin package manager.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import os
import shutil
import stat
import subprocess
import tempfile
import zipfile
from pathlib import Path
from typing import Iterable

REPO_ROOT = Path(__file__).resolve().parents[1]
PLUGIN_DIR = REPO_ROOT / "plugins" / "StashHybridRecommendations"
PACKAGE_SOURCE_DIR = PLUGIN_DIR / "package-source"
ZIP_DIR = PACKAGE_SOURCE_DIR / "zips"
ENGINE_DIR = REPO_ROOT / "engine" / "go"
ENGINE_MAIN = "./cmd/stash-hybrid-engine"
REPO_URL = "https://github.com/gomeng-dev/stash-recommendation-server"
DEFAULT_VERSION = "0.3.1"
DEFAULT_DATE = "2026-05-07 00:00:00"
ZIP_TIMESTAMP = (2020, 1, 1, 0, 0, 0)

TARGETS = [
    {
        "id": "linux-amd64",
        "name": "Linux amd64",
        "goos": "linux",
        "goarch": "amd64",
        "plugin_id": "StashHybridRecommendationsEngineLinuxAmd64",
        "package_id": "stash-hybrid-recommendations-engine-linux-amd64",
    },
    {
        "id": "linux-arm64v8",
        "name": "Linux arm64 v8",
        "goos": "linux",
        "goarch": "arm64",
        "plugin_id": "StashHybridRecommendationsEngineLinuxArm64v8",
        "package_id": "stash-hybrid-recommendations-engine-linux-arm64v8",
    },
    {
        "id": "linux-arm32v7",
        "name": "Linux arm32 v7",
        "goos": "linux",
        "goarch": "arm",
        "goarm": "7",
        "plugin_id": "StashHybridRecommendationsEngineLinuxArm32v7",
        "package_id": "stash-hybrid-recommendations-engine-linux-arm32v7",
    },
    {
        "id": "linux-arm32v6",
        "name": "Linux arm32 v6",
        "goos": "linux",
        "goarch": "arm",
        "goarm": "6",
        "plugin_id": "StashHybridRecommendationsEngineLinuxArm32v6",
        "package_id": "stash-hybrid-recommendations-engine-linux-arm32v6",
    },
    {
        "id": "macos-arm64",
        "name": "macOS arm64",
        "goos": "darwin",
        "goarch": "arm64",
        "plugin_id": "StashHybridRecommendationsEngineMacosArm64",
        "package_id": "stash-hybrid-recommendations-engine-macos-arm64",
    },
    {
        "id": "macos-amd64",
        "name": "macOS amd64",
        "goos": "darwin",
        "goarch": "amd64",
        "plugin_id": "StashHybridRecommendationsEngineMacosAmd64",
        "package_id": "stash-hybrid-recommendations-engine-macos-amd64",
    },
    {
        "id": "windows-amd64",
        "name": "Windows amd64",
        "goos": "windows",
        "goarch": "amd64",
        "plugin_id": "StashHybridRecommendationsEngineWindowsAmd64",
        "package_id": "stash-hybrid-recommendations-engine-windows-amd64",
        "exe": True,
    },
    {
        "id": "windows-arm64",
        "name": "Windows arm64",
        "goos": "windows",
        "goarch": "arm64",
        "plugin_id": "StashHybridRecommendationsEngineWindowsArm64",
        "package_id": "stash-hybrid-recommendations-engine-windows-arm64",
        "exe": True,
    },
    {
        "id": "freebsd-amd64",
        "name": "FreeBSD amd64",
        "goos": "freebsd",
        "goarch": "amd64",
        "plugin_id": "StashHybridRecommendationsEngineFreebsdAmd64",
        "package_id": "stash-hybrid-recommendations-engine-freebsd-amd64",
    },
    {
        "id": "freebsd-arm64",
        "name": "FreeBSD arm64",
        "goos": "freebsd",
        "goarch": "arm64",
        "plugin_id": "StashHybridRecommendationsEngineFreebsdArm64",
        "package_id": "stash-hybrid-recommendations-engine-freebsd-arm64",
    },
]


def source_date() -> str:
    epoch = os.environ.get("SOURCE_DATE_EPOCH")
    if epoch:
        try:
            instant = dt.datetime.fromtimestamp(int(epoch), tz=dt.UTC)
            return instant.strftime("%Y-%m-%d 00:00:00")
        except ValueError:
            pass
    return os.environ.get("STASH_HYBRID_PACKAGE_DATE", DEFAULT_DATE)


def package_version() -> str:
    return os.environ.get("STASH_HYBRID_PACKAGE_VERSION", DEFAULT_VERSION)


def write_zip_entry(zf: zipfile.ZipFile, arcname: str, data: bytes, mode: int) -> None:
    info = zipfile.ZipInfo(arcname.replace(os.sep, "/"), ZIP_TIMESTAMP)
    info.compress_type = zipfile.ZIP_DEFLATED
    info.create_system = 3
    info.external_attr = (mode & 0xFFFF) << 16
    zf.writestr(info, data)


def zip_files(zip_path: Path, entries: Iterable[tuple[str, Path | bytes, int]]) -> None:
    zip_path.parent.mkdir(parents=True, exist_ok=True)
    tmp = zip_path.with_suffix(zip_path.suffix + ".tmp")
    with zipfile.ZipFile(tmp, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as zf:
        for arcname, source, mode in sorted(entries, key=lambda item: item[0]):
            data = source if isinstance(source, bytes) else source.read_bytes()
            write_zip_entry(zf, arcname, data, mode)
    tmp.replace(zip_path)


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def engine_yaml(target: dict[str, object], executable: str, version: str) -> bytes:
    text = f"""name: Stash Hybrid Recommendations Engine - {target['name']}
description: Managed raw-plugin engine for Stash Hybrid Recommendations ({target['id']}).
version: {version}
url: {REPO_URL}
exec:
  - {executable}
interface: raw
errLog: info
tasks:
  - name: Bootstrap recommendations
    description: Create the integrated recommendation DB, index scenes, and build the hybrid-v3-lite cache.
    defaultArgs:
      mode: bootstrap
  - name: Index scene metadata
    description: Re-fetch Stash scene metadata into the integrated recommendation DB.
    defaultArgs:
      mode: index-scenes
  - name: Rebuild recommendation cache
    description: Rebuild the hybrid-v3-lite recommendation cache from already indexed scenes.
    defaultArgs:
      mode: build-cache
  - name: Prune deleted scenes
    description: Compare current Stash scene IDs with the local DB, remove deleted scenes, and rebuild only affected recommendation rows.
    defaultArgs:
      mode: prune-deleted-scenes
  - name: Import database file
    description: Back up the current engine DB, import a readable SQLite recommendation DB file, and migrate it if needed.
    defaultArgs:
      mode: import-db
  - name: Build dev test DB (100 scenes)
    description: Development only. Move aside any current engine DB, index exactly 100 Stash scenes, and build a small hybrid-v3-lite cache.
    defaultArgs:
      mode: dev-test-100
      maxScenes: 100
      limitScenes: 100
      topN: 50
"""
    return text.encode("utf-8")


def build_engine_binary(target: dict[str, object], out: Path) -> None:
    env = os.environ.copy()
    env.update({
        "CGO_ENABLED": "0",
        "GOOS": str(target["goos"]),
        "GOARCH": str(target["goarch"]),
    })
    if target.get("goarm"):
        env["GOARM"] = str(target["goarm"])
    cmd = [
        "go",
        "build",
        "-trimpath",
        "-ldflags=-buildid=",
        "-o",
        str(out),
        ENGINE_MAIN,
    ]
    subprocess.run(cmd, cwd=ENGINE_DIR, env=env, check=True)
    out.chmod(out.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


def build_engine_package(target: dict[str, object], version: str) -> tuple[dict[str, object], Path]:
    executable = "stash-hybrid-engine.exe" if target.get("exe") else "stash-hybrid-engine"
    zip_path = ZIP_DIR / f"{target['package_id']}.zip"
    with tempfile.TemporaryDirectory(prefix="stash-hybrid-engine-build-") as tmpdir:
        binary_path = Path(tmpdir) / executable
        build_engine_binary(target, binary_path)
        zip_files(
            zip_path,
            [
                (f"{target['plugin_id']}.yml", engine_yaml(target, executable, version), 0o644),
                (executable, binary_path, 0o755),
                ("data/.gitkeep", b"", 0o644),
            ],
        )
    entry = {
        "id": target["package_id"],
        "name": f"Stash Hybrid Recommendations Engine - {target['name']}",
        "version": version,
        "date": source_date(),
        "path": f"zips/{zip_path.name}",
        "sha256": sha256(zip_path),
        "metadata": {
            "target": target["id"],
            "enginePluginId": target["plugin_id"],
            "packageRole": "engine",
            "binary": executable,
        },
    }
    return entry, zip_path


def build_onboarding_package(version: str) -> tuple[dict[str, object], Path]:
    zip_path = ZIP_DIR / "stash-hybrid-recommendations.zip"
    files = [
        "StashHybridRecommendations.yml",
        "stashHybridRecommendationsCore.js",
        "stashHybridRecommendations.js",
        "stashHybridRecommendations.css",
    ]
    zip_files(zip_path, [(name, PLUGIN_DIR / name, 0o644) for name in files])
    entry = {
        "id": "stash-hybrid-recommendations",
        "name": "Stash Hybrid Recommendations",
        "version": version,
        "date": source_date(),
        "path": f"zips/{zip_path.name}",
        "sha256": sha256(zip_path),
        "metadata": {
            "packageRole": "onboarding-ui",
            "pluginId": "StashHybridRecommendations",
        },
    }
    return entry, zip_path


def yaml_scalar(value: object) -> str:
    if isinstance(value, bool):
        return "true" if value else "false"
    text = str(value)
    if text == "" or any(ch in text for ch in [":", "#", "'", "\n", "{", "}", "[", "]"]):
        return "'" + text.replace("'", "''") + "'"
    return text


def write_index(entries: list[dict[str, object]], filename: str, aliases: Iterable[str] = ()) -> None:
    lines: list[str] = []
    for entry in entries:
        lines.append(f"- id: {yaml_scalar(entry['id'])}")
        for key in ["name", "version", "date", "path", "sha256"]:
            lines.append(f"  {key}: {yaml_scalar(entry[key])}")
        metadata = entry.get("metadata")
        if isinstance(metadata, dict) and metadata:
            lines.append("  metadata:")
            for mkey, mvalue in metadata.items():
                lines.append(f"    {mkey}: {yaml_scalar(mvalue)}")
    PACKAGE_SOURCE_DIR.mkdir(parents=True, exist_ok=True)
    text = "\n".join(lines) + "\n"
    for name in (filename, *aliases):
        (PACKAGE_SOURCE_DIR / name).write_text(text, encoding="utf-8")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", default=package_version(), help="package version for generated assets")
    parser.add_argument("--engines-only", action="store_true", help="skip the JS onboarding plugin package")
    parser.add_argument("--target", action="append", choices=[str(t["id"]) for t in TARGETS], help="build only one target; may be repeated")
    args = parser.parse_args()

    selected = [t for t in TARGETS if not args.target or t["id"] in set(args.target)]
    if not selected:
        raise SystemExit("no targets selected")

    ZIP_DIR.mkdir(parents=True, exist_ok=True)
    onboarding_entries: list[dict[str, object]] = []
    engine_entries: list[dict[str, object]] = []
    built: list[Path] = []

    if not args.engines_only:
        entry, path = build_onboarding_package(args.version)
        onboarding_entries.append(entry)
        built.append(path)

    for target in selected:
        entry, path = build_engine_package(target, args.version)
        engine_entries.append(entry)
        built.append(path)

    if onboarding_entries:
        write_index(onboarding_entries, "index.yml", aliases=("index",))
        print(f"wrote {PACKAGE_SOURCE_DIR / 'index.yml'}")
        print(f"wrote {PACKAGE_SOURCE_DIR / 'index'}")
    write_index(engine_entries, "engines.yml", aliases=("engines",))
    print(f"wrote {PACKAGE_SOURCE_DIR / 'engines.yml'}")
    print(f"wrote {PACKAGE_SOURCE_DIR / 'engines'}")
    for path in built:
        print(f"wrote {path.relative_to(REPO_ROOT)} {sha256(path)}")


if __name__ == "__main__":
    main()
