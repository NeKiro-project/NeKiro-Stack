#!/usr/bin/env python3
"""Build the bilingual MkDocs source tree from the Stack README."""

from __future__ import annotations

import argparse
import posixpath
import re
import shutil
import sys
import urllib.parse
from pathlib import Path
from typing import NoReturn


REPO_ROOT = Path(__file__).resolve().parents[1]
WIKI_ROOT = REPO_ROOT / "repowiki"
DEFAULT_OUTPUT = REPO_ROOT / ".repowiki-site"
SOURCE_URL_PREFIX = "https://github.com/NeKiro-project/NeKiro-Stack/blob/main/"
MARKDOWN_LINK = re.compile(r"(?<!!)(\[[^\]]+\])\(([^)]+)\)")
H1 = re.compile(r"^#\s+(.+?)\s*$")
SOURCE = "README.md"
TARGET = "source-docs/overview.md"


def fail(message: str) -> NoReturn:
    raise ValueError(message)


def document_title(path: Path) -> str:
    for line in path.read_text(encoding="utf-8").splitlines():
        match = H1.match(line)
        if match:
            return match.group(1).strip()
    fail(f"source document has no level-one heading: {path.relative_to(REPO_ROOT)}")


def source_url(source: str) -> str:
    return SOURCE_URL_PREFIX + urllib.parse.quote(source, safe="/")


def rewrite_links(text: str) -> str:
    def replace(match: re.Match[str]) -> str:
        destination = match.group(2).strip()
        if destination.startswith(("http://", "https://", "mailto:", "#", "<")):
            return match.group(0)
        parts = destination.split(None, 1)
        link_target = parts[0]
        suffix = f" {parts[1]}" if len(parts) == 2 else ""
        fragment = ""
        if "#" in link_target:
            link_target, raw_fragment = link_target.split("#", 1)
            fragment = f"#{raw_fragment}"
        if not link_target.endswith(".md"):
            return match.group(0)
        resolved = posixpath.normpath(posixpath.join("README.md", link_target))
        link = source_url(resolved)
        return f"{match.group(1)}({link}{fragment}{suffix})"

    return MARKDOWN_LINK.sub(replace, text)


def source_page(language: str) -> str:
    if language == "zh":
        note = (
            '<div class="source-note">Canonical source：'
            f'<a href="{source_url(SOURCE)}"><code>{SOURCE}</code></a>。'
            "本页保留英文 canonical 正文，中文导航和入口已提供。</div>"
        )
    else:
        note = (
            '<div class="source-note">Canonical source: '
            f'<a href="{source_url(SOURCE)}"><code>{SOURCE}</code></a>. '
            "This page is rendered from the repository README during the MkDocs build.</div>"
        )
    return f"{note}\n\n{rewrite_links((REPO_ROOT / SOURCE).read_text(encoding='utf-8'))}"


def copy_tracked_wiki(output: Path) -> None:
    for path in WIKI_ROOT.rglob("*"):
        if path.is_dir():
            continue
        relative = path.relative_to(WIKI_ROOT)
        if relative.parts[0] == "zh":
            continue
        if relative.parts[0] == "assets":
            destination = output / relative
        elif path.suffix == ".md":
            destination = output / "en" / relative
        else:
            fail(f"unsupported tracked RepoWiki file: {relative}")
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, destination)
    for path in (WIKI_ROOT / "zh").rglob("*"):
        if path.is_dir():
            continue
        relative = path.relative_to(WIKI_ROOT)
        destination = output / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, destination)


def validate() -> None:
    for relative in ("index.md", "zh/index.md", "assets/stylesheets/extra.css"):
        if not (WIKI_ROOT / relative).is_file():
            fail(f"missing RepoWiki input: repowiki/{relative}")
    path = REPO_ROOT / SOURCE
    if not path.is_file():
        fail("missing source document: README.md")
    document_title(path)
    for wiki_path in WIKI_ROOT.rglob("*.md"):
        text = wiki_path.read_text(encoding="utf-8")
        if "{{" in text or "relative_url" in text:
            fail(f"Jekyll/Liquid link remains in RepoWiki source: {wiki_path.relative_to(REPO_ROOT)}")


def build(output: Path) -> None:
    if output.exists():
        if output.is_dir():
            shutil.rmtree(output)
        else:
            output.unlink()
    output.mkdir(parents=True, exist_ok=True)
    copy_tracked_wiki(output)
    for language in ("en", "zh"):
        generated_root = output / language / "source-docs"
        generated_root.mkdir(parents=True, exist_ok=True)
        if language == "zh":
            index = "# 源文档\n\n以下页面从 Stack 仓库的 canonical README 生成。\n"
        else:
            index = "# Source documentation\n\nThis page is generated from the canonical Stack README.\n"
        (generated_root / "index.md").write_text(index, encoding="utf-8")
        (output / language / TARGET).write_text(source_page(language), encoding="utf-8")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument("--check", action="store_true")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        validate()
        if args.check:
            print("RepoWiki check passed: 1 source document, 2 locales")
        else:
            output = args.output if args.output.is_absolute() else REPO_ROOT / args.output
            build(output)
            print(f"MkDocs source generated: {output}")
    except ValueError as error:
        print(f"RepoWiki build failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
