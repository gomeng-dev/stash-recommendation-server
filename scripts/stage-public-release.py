#!/usr/bin/env python3
"""Stage the sanitized public release tree for Stash Hybrid Recommendations.

The private development repository contains plans, reports, prompts, local
databases, and other files that must never be mirrored to the public repository.
This script copies the product source code plus Stash package release assets,
while excluding security-sensitive and private-development material.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import zipfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
PACKAGE_SOURCE_DIR = REPO_ROOT / "plugins" / "StashHybridRecommendations" / "package-source"
DEFAULT_OUT_DIR = REPO_ROOT / "dist" / "public-release"

ALLOWED_ROOT_FILES = {
    "README.md",
    "package.json",
    "package-lock.json",
    "index",
    "index.yml",
    "engines",
    "engines.yml",
    ".gitignore",
    "release-manifest.json",
}
ALLOWED_ROOT_DIRS = {"engine", "plugins", "scripts", "zips"}
DISALLOWED_NAMES = {
    ".env",
    ".env.local",
    ".github",
    "AGENTS.md",
    "Agents.md",
    "agents.md",
    "CLAUDE.md",
    "AUTO_QUEUE.md",
    "data",
    "docs",
    "logs",
    "node_modules",
    "dist",
    "__pycache__",
}
DISALLOWED_ZIP_ENTRY_NAMES = DISALLOWED_NAMES - {"data"}
SOURCE_SKIP_NAMES = DISALLOWED_NAMES | {"stash-hybrid-engine", "stash-hybrid-engine.exe"}
PLUGIN_PUBLIC_FILES = [
    "StashHybridRecommendations.yml",
    "stashHybridRecommendations.css",
    "stashHybridRecommendations.js",
    "stashHybridRecommendationsCore.js",
    "tests/core.test.mjs",
    "tests/ui-render-safety.test.mjs",
]
SCRIPT_PUBLIC_FILES = [
    "build-stash-plugin-packages.py",
    "secret-scan.mjs",
    "stage-public-release.py",
    "validate-stash-plugin-packages.py",
]


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def copy_file(src: Path, dst: Path) -> None:
    if not src.exists():
        raise SystemExit(f"missing required source file: {src}")
    if src.is_symlink():
        raise SystemExit(f"symlink source is not allowed: {src}")
    dst.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(src, dst)


def copy_tree(src: Path, dst: Path) -> None:
    if not src.exists():
        raise SystemExit(f"missing required source directory: {src}")
    for path in sorted(src.rglob("*")):
        rel = path.relative_to(src)
        if path.is_symlink():
            raise SystemExit(f"symlink source is not allowed: {path}")
        if any(part in SOURCE_SKIP_NAMES for part in rel.parts):
            continue
        if path.is_dir():
            continue
        copy_file(path, dst / rel)


def assert_safe_zip(path: Path) -> None:
    with zipfile.ZipFile(path) as zf:
        bad = zf.testzip()
        if bad:
            raise SystemExit(f"corrupt zip member in {path}: {bad}")
        for info in zf.infolist():
            parts = [part for part in info.filename.replace("\\", "/").split("/") if part]
            if info.filename.startswith("/") or ".." in parts:
                raise SystemExit(f"unsafe zip member path in {path}: {info.filename}")
            if any(part in DISALLOWED_ZIP_ENTRY_NAMES for part in parts):
                raise SystemExit(f"disallowed zip member in {path}: {info.filename}")


def stage(out_dir: Path, version: str, source_tag: str, published_at: str) -> None:
    if out_dir.exists():
        shutil.rmtree(out_dir)
    out_dir.mkdir(parents=True)

    copy_file(REPO_ROOT / "README.md", out_dir / "README.md")
    copy_file(REPO_ROOT / "package.json", out_dir / "package.json")
    copy_file(REPO_ROOT / "package-lock.json", out_dir / "package-lock.json")
    copy_tree(REPO_ROOT / "engine" / "go", out_dir / "engine" / "go")
    for name in PLUGIN_PUBLIC_FILES:
        copy_file(REPO_ROOT / "plugins" / "StashHybridRecommendations" / name, out_dir / "plugins" / "StashHybridRecommendations" / name)
    for name in SCRIPT_PUBLIC_FILES:
        copy_file(REPO_ROOT / "scripts" / name, out_dir / "scripts" / name)
    for name in ["index", "index.yml", "engines", "engines.yml"]:
        copy_file(PACKAGE_SOURCE_DIR / name, out_dir / name)

    zip_src = PACKAGE_SOURCE_DIR / "zips"
    zip_dst = out_dir / "zips"
    if not zip_src.exists():
        raise SystemExit(f"missing required zip directory: {zip_src}")
    shutil.copytree(zip_src, zip_dst)

    (out_dir / ".gitignore").write_text(
        ".DS_Store\n*.tmp\nengine/go/stash-hybrid-engine\nengine/go/stash-hybrid-engine.exe\n",
        encoding="utf-8",
    )

    zip_entries = []
    for path in sorted(zip_dst.iterdir()):
        if path.is_symlink():
            raise SystemExit(f"symlink is not allowed in public zips: {path}")
        if not path.is_file() or path.suffix != ".zip":
            raise SystemExit(f"public zips directory may contain only .zip files: {path}")
        assert_safe_zip(path)
        zip_entries.append({
            "path": str(path.relative_to(out_dir)),
            "sha256": sha256(path),
            "size": path.stat().st_size,
        })

    manifest = {
        "publicRepository": "gomeng-dev/stash-recommendation-server",
        "packageSourceUrl": "https://raw.githubusercontent.com/gomeng-dev/stash-recommendation-server/main/index",
        "engineSourceUrl": "https://raw.githubusercontent.com/gomeng-dev/stash-recommendation-server/main/engines",
        "packageVersion": version,
        "sourceTag": source_tag,
        "publishedAt": published_at,
        "files": sorted(str(p.relative_to(out_dir)) for p in out_dir.rglob("*") if p.is_file()),
        "zips": zip_entries,
    }
    (out_dir / "release-manifest.json").write_text(json.dumps(manifest, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def validate(out_dir: Path) -> None:
    if not out_dir.exists():
        raise SystemExit(f"staged output does not exist: {out_dir}")

    root_items = {p.name for p in out_dir.iterdir()}
    disallowed = sorted(root_items & DISALLOWED_NAMES)
    if disallowed:
        raise SystemExit(f"disallowed public root entries: {disallowed}")

    for item in out_dir.iterdir():
        if item.is_symlink():
            raise SystemExit(f"symlink is not allowed in public root: {item.name}")
        if item.is_dir() and item.name not in ALLOWED_ROOT_DIRS:
            raise SystemExit(f"unexpected public root directory: {item.name}")
        if item.is_file() and item.name not in ALLOWED_ROOT_FILES:
            raise SystemExit(f"unexpected public root file: {item.name}")

    required = [
        "README.md",
        "package.json",
        "package-lock.json",
        "index",
        "index.yml",
        "engines",
        "engines.yml",
        "release-manifest.json",
    ]
    for name in required:
        if not (out_dir / name).is_file():
            raise SystemExit(f"missing public file: {name}")
    required_dirs = [
        "engine/go",
        "plugins/StashHybridRecommendations",
        "scripts",
        "zips",
    ]
    for name in required_dirs:
        if not (out_dir / name).is_dir():
            raise SystemExit(f"missing public directory: {name}")
    if (out_dir / "plugins" / "StashHybridRecommendations" / "package-source").exists():
        raise SystemExit("plugins/StashHybridRecommendations/package-source should not be duplicated in the public source tree")
    for binary in ["engine/go/stash-hybrid-engine", "engine/go/stash-hybrid-engine.exe"]:
        if (out_dir / binary).exists():
            raise SystemExit(f"built engine binary must not be copied as source: {binary}")

    zip_dir = out_dir / "zips"
    for item in zip_dir.iterdir():
        if item.is_symlink():
            raise SystemExit(f"symlink is not allowed in public zips: {item.name}")
        if not item.is_file() or item.suffix != ".zip":
            raise SystemExit(f"unexpected public zip directory entry: {item.name}")
    zips = sorted(zip_dir.glob("*.zip"))
    if len(zips) < 2:
        raise SystemExit("expected onboarding and engine zip assets in public zips/")
    for path in zips:
        assert_safe_zip(path)

    forbidden_anywhere = []
    for p in out_dir.rglob("*"):
        if any(part in DISALLOWED_NAMES for part in p.relative_to(out_dir).parts):
            forbidden_anywhere.append(str(p.relative_to(out_dir)))
    if forbidden_anywhere:
        raise SystemExit(f"forbidden public paths: {forbidden_anywhere}")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT_DIR, help="output directory for sanitized public tree")
    parser.add_argument("--check", action="store_true", help="validate an existing staged tree without copying")
    parser.add_argument("--version", default=os.environ.get("STASH_HYBRID_PACKAGE_VERSION", ""), help="package version for release-manifest.json")
    parser.add_argument("--source-tag", default=os.environ.get("GITHUB_REF_NAME", ""), help="source version tag for release-manifest.json")
    parser.add_argument("--published-at", default="", help="UTC timestamp for release-manifest.json")
    args = parser.parse_args()

    out_dir = args.out.resolve()
    if not args.check:
        stage(out_dir, args.version, args.source_tag, args.published_at)
    validate(out_dir)
    files = sorted(str(p.relative_to(out_dir)) for p in out_dir.rglob("*") if p.is_file())
    print(f"public release staging ok: {out_dir}")
    for file in files:
        print(file)


if __name__ == "__main__":
    main()
