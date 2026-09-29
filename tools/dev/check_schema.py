#!/usr/bin/env python3
"""Dev-only: check policy fixtures against schema/policy.v1.schema.json.

This is NOT the production validator (that is `git-policy validate`, Go).
It checks that the structural JSON Schema agrees with the fixture expectations:

  examples/*.yaml                         -> must pass
  testdata/policies/schema-invalid/*.yaml -> must FAIL the schema
  testdata/policies/semantic-invalid/*    -> must PASS the schema
                                             (they are caught by the Go validator)

Requires: python3, PyYAML, jsonschema (>= 4). Exit code 0 when all expectations hold.
"""
import json
import pathlib
import sys

import jsonschema
import yaml

ROOT = pathlib.Path(__file__).resolve().parents[2]


class StrLoader(yaml.SafeLoader):
    """SafeLoader that keeps YAML dates as strings, like the Go decoder does."""


StrLoader.yaml_implicit_resolvers = {
    k: [r for r in v if r[0] != "tag:yaml.org,2002:timestamp"]
    for k, v in yaml.SafeLoader.yaml_implicit_resolvers.items()
}


def expectation(path):
    first = path.read_text().splitlines()[0]
    return first.removeprefix("# expect:").strip() if first.startswith("# expect:") else ""


def main():
    schema = json.loads((ROOT / "schema/policy.v1.schema.json").read_text())
    validator = jsonschema.Draft202012Validator(schema)
    groups = [
        ("valid", sorted((ROOT / "examples").glob("*.yaml")), True),
        ("schema-invalid", sorted((ROOT / "testdata/policies/schema-invalid").glob("*.yaml")), False),
        ("semantic-invalid", sorted((ROOT / "testdata/policies/semantic-invalid").glob("*.yaml")), True),
    ]
    failures = 0
    for name, files, want_pass in groups:
        print(f"== {name}")
        for f in files:
            doc = yaml.load(f.read_text(), Loader=StrLoader)
            errors = sorted(validator.iter_errors(doc), key=lambda e: list(e.path))
            passed = not errors
            ok = passed == want_pass
            failures += not ok
            status = "ok  " if ok else "FAIL"
            detail = "schema-valid" if passed else f"schema error at /{'/'.join(map(str, errors[0].path))}: {errors[0].message[:90]}"
            exp = expectation(f)
            print(f"  [{status}] {f.name:48} {detail}" + (f"   ({exp})" if exp else ""))
    print(f"\n{'ALL EXPECTATIONS MET' if not failures else f'{failures} EXPECTATION(S) FAILED'}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
