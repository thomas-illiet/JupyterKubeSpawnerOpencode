#!/usr/bin/env python3
import json
import pathlib
import sys
import xml.etree.ElementTree as ET

root = pathlib.Path(__file__).resolve().parents[1]
errors = []

try:
    import yaml
except ImportError:
    yaml = None

for path in (root / "docs" / "images").glob("*.svg"):
    try:
        ET.parse(path)
    except ET.ParseError as error:
        errors.append(f"{path.relative_to(root)}: {error}")

for path in (root / "charts").glob("**/*.json"):
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

if errors:
    print("\n".join(errors), file=sys.stderr)
    raise SystemExit(1)
print("Documentation artifacts are valid")
