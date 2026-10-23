#!/usr/bin/env python3
import json
import pathlib
import re
import sys
import urllib.parse
import xml.etree.ElementTree as ET

root = pathlib.Path(__file__).resolve().parents[1]
errors = []

ignored_documentation_directories = {
    ".agents",
    ".codex",
    ".git",
    ".hg",
    ".mypy_cache",
    ".output",
    ".pytest_cache",
    ".ruff_cache",
    ".svn",
    ".venv",
    "__pycache__",
    "bin",
    "coverage",
    "dist",
    "node_modules",
    "venv",
}
inline_link_pattern = re.compile(
    r'''!?\[[^\]\n]*\]\(\s*'''
    r'''(<[^>\n]+>|(?:\\.|[^\s)\n])+)'''
    r'''(?:\s+(?:"[^"\n]*"|'[^'\n]*'|\([^\)\n]*\)))?\s*\)'''
)
reference_link_pattern = re.compile(
    r"^[ \t]{0,3}\[[^\]\n]+\]:[ \t]*(<[^>\n]+>|\S+)", re.MULTILINE
)
fence_pattern = re.compile(r"^[ ]{0,3}(`{3,}|~{3,})")


def local_name(tag):
    return tag.rsplit("}", 1)[-1]


def text_content(element):
    return "".join(element.itertext()).strip()


def markdown_without_fenced_code(value):
    lines = []
    fence_character = ""
    fence_length = 0
    for line in value.splitlines(keepends=True):
        match = fence_pattern.match(line)
        if match:
            marker = match.group(1)
            if not fence_character:
                fence_character = marker[0]
                fence_length = len(marker)
            elif marker[0] == fence_character and len(marker) >= fence_length:
                fence_character = ""
                fence_length = 0
            lines.append("\n")
            continue
        lines.append("\n" if fence_character else line)
    return "".join(lines)


def markdown_destination(value):
    value = value.strip()
    if value.startswith("<"):
        closing = value.find(">")
        return value[1:closing] if closing != -1 else value
    return value.split(maxsplit=1)[0] if value else ""


def validate_local_target(markdown, raw_target):
    target = markdown_destination(raw_target)
    if not target or target.startswith("#"):
        return
    try:
        parsed = urllib.parse.urlsplit(target)
    except ValueError as error:
        errors.append(f"{markdown.relative_to(root)}: invalid link {target!r}: {error}")
        return
    if parsed.scheme or parsed.netloc:
        return
    if not parsed.path:
        return
    if re.search(r"%(?![0-9A-Fa-f]{2})", parsed.path):
        errors.append(
            f"{markdown.relative_to(root)}: invalid percent encoding in local target {target!r}"
        )
        return
    try:
        decoded_path = urllib.parse.unquote_to_bytes(parsed.path).decode("utf-8")
    except UnicodeDecodeError as error:
        errors.append(
            f"{markdown.relative_to(root)}: local target {target!r} is not valid UTF-8: {error}"
        )
        return

    if decoded_path.startswith("/"):
        candidate = root / decoded_path.lstrip("/")
    else:
        candidate = markdown.parent / decoded_path
    try:
        resolved = candidate.resolve(strict=False)
    except (OSError, RuntimeError, ValueError) as error:
        errors.append(
            f"{markdown.relative_to(root)}: invalid local target {target!r}: {error}"
        )
        return
    try:
        relative_target = resolved.relative_to(root)
    except ValueError:
        errors.append(
            f"{markdown.relative_to(root)}: local target {target!r} escapes the repository"
        )
        return
    try:
        target_exists = resolved.exists()
    except (OSError, ValueError) as error:
        errors.append(
            f"{markdown.relative_to(root)}: invalid local target {target!r}: {error}"
        )
        return
    if not target_exists:
        errors.append(
            f"{markdown.relative_to(root)}: missing local target {target!r} "
            f"(resolved to {relative_target})"
        )


def validate_markdown_links():
    markdown_files = sorted(
        path
        for path in root.rglob("*.md")
        if not any(part in ignored_documentation_directories for part in path.relative_to(root).parts)
    )
    for markdown in markdown_files:
        try:
            content = markdown.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError) as error:
            errors.append(f"{markdown.relative_to(root)}: {error}")
            continue
        content = markdown_without_fenced_code(content)
        targets = [match.group(1) for match in inline_link_pattern.finditer(content)]
        targets.extend(match.group(1) for match in reference_link_pattern.finditer(content))
        for target in targets:
            validate_local_target(markdown, target)


try:
    import yaml
except ImportError:
    yaml = None

for path in sorted((root / "docs" / "images").glob("*.svg")):
    try:
        document = ET.parse(path)
    except (OSError, ET.ParseError) as error:
        errors.append(f"{path.relative_to(root)}: {error}")
        continue
    svg = document.getroot()
    if local_name(svg.tag) != "svg":
        errors.append(f"{path.relative_to(root)}: root element must be <svg>")
        continue
    if svg.get("role") != "img":
        errors.append(f'{path.relative_to(root)}: root <svg> must declare role="img"')

    labelled_by = set(svg.get("aria-labelledby", "").split())
    title = next(
        (element for element in svg if local_name(element.tag) == "title" and text_content(element)),
        None,
    )
    description = next(
        (element for element in svg if local_name(element.tag) == "desc" and text_content(element)),
        None,
    )
    if title is None:
        errors.append(f"{path.relative_to(root)}: root <svg> requires a non-empty <title>")
    elif not title.get("id") or title.get("id") not in labelled_by:
        errors.append(
            f"{path.relative_to(root)}: aria-labelledby must reference the non-empty <title> id"
        )
    if description is None:
        errors.append(f"{path.relative_to(root)}: root <svg> requires a non-empty <desc>")
    elif not description.get("id") or description.get("id") not in labelled_by:
        errors.append(
            f"{path.relative_to(root)}: aria-labelledby must reference the non-empty <desc> id"
        )
    if (
        title is not None
        and description is not None
        and title.get("id")
        and title.get("id") == description.get("id")
    ):
        errors.append(f"{path.relative_to(root)}: <title> and <desc> ids must be distinct")

for path in sorted((root / "charts").glob("**/*.json")):
    try:
        json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        errors.append(f"{path.relative_to(root)}: {error}")

openapi = root / "docs" / "openapi.yaml"
if yaml is not None:
    try:
        document = yaml.safe_load(openapi.read_text(encoding="utf-8"))
        if not isinstance(document, dict) or not str(document.get("openapi", "")).startswith("3."):
            errors.append("docs/openapi.yaml: missing OpenAPI 3.x declaration")
    except (OSError, yaml.YAMLError) as error:
        errors.append(f"docs/openapi.yaml: {error}")
elif not openapi.read_text(encoding="utf-8").startswith("openapi: 3."):
    errors.append("docs/openapi.yaml: missing OpenAPI 3.x declaration")

validate_markdown_links()

if errors:
    print("\n".join(errors), file=sys.stderr)
    raise SystemExit(1)
print("Documentation artifacts are valid")
