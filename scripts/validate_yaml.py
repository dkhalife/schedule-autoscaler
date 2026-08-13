#!/usr/bin/env python3
"""Parse repository YAML and reject duplicate mapping keys."""

from __future__ import annotations

import pathlib
import sys
from typing import Iterable

import yaml


class UniqueKeyLoader(yaml.SafeLoader):
    pass


def construct_mapping(loader: UniqueKeyLoader, node: yaml.MappingNode, deep: bool = False):
    mapping = {}
    for key_node, value_node in node.value:
        key = loader.construct_object(key_node, deep=deep)
        if key in mapping:
            raise yaml.constructor.ConstructorError(
                "while constructing a mapping",
                node.start_mark,
                f"found duplicate key {key!r}",
                key_node.start_mark,
            )
        mapping[key] = loader.construct_object(value_node, deep=deep)
    return mapping


UniqueKeyLoader.add_constructor(
    yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, construct_mapping
)


def repository_yaml(root: pathlib.Path) -> Iterable[pathlib.Path]:
    roots = [root / "config", root / "examples", root / "charts"]
    for directory in roots:
        if not directory.exists():
            continue
        for path in sorted(directory.rglob("*")):
            if path.suffix in {".yaml", ".yml"} and "templates" not in path.parts:
                yield path


def validate(name: str, text: str) -> int:
    count = 0
    try:
        for document in yaml.load_all(text, Loader=UniqueKeyLoader):
            if document is None:
                continue
            count += 1
            if not isinstance(document, dict):
                raise ValueError("document root must be a mapping")
    except (yaml.YAMLError, ValueError) as error:
        print(f"{name}: {error}", file=sys.stderr)
        return -1
    return count


def main() -> int:
    if len(sys.argv) == 2 and sys.argv[1] == "-":
        result = validate("<stdin>", sys.stdin.read())
        if result < 0:
            return 1
        print(f"validated {result} rendered YAML document(s)")
        return 0
    if len(sys.argv) > 1:
        paths = [pathlib.Path(argument) for argument in sys.argv[1:]]
    else:
        paths = list(repository_yaml(pathlib.Path.cwd()))

    total = 0
    failed = False
    for path in paths:
        result = validate(str(path), path.read_text(encoding="utf-8"))
        if result < 0:
            failed = True
        else:
            total += result

    canonical = pathlib.Path("config/crd/bases/dkhalife.dev_schedules.yaml")
    chart_copy = pathlib.Path(
        "charts/schedule-autoscaler/crds/dkhalife.dev_schedules.yaml"
    )
    if canonical.exists() and chart_copy.exists():
        if canonical.read_bytes() != chart_copy.read_bytes():
            print("Helm CRDs differ from config/crd canonical copy", file=sys.stderr)
            failed = True

    if failed:
        return 1
    print(f"validated {len(paths)} file(s), {total} YAML document(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
