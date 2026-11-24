#!/usr/bin/env python3
"""Validate the Whirlwind Plan acceptance ledger; this does not run tests."""

import argparse
import json
from pathlib import Path
import re
import sys


REPO = Path(__file__).resolve().parent.parent
DESIGN = REPO / "docs" / "design"
SHA = re.compile(r"[a-f0-9]{40}")
PR = re.compile(r"https://github\.com/nirmalhk7/whirlwind/pull/[1-9][0-9]*")
ISSUE = re.compile(r"https://github\.com/nirmalhk7/whirlwind/issues/[1-9][0-9]*")
SCOPES = {"applicable", "partly_deferred", "partly_waived", "deferred"}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, f"duplicate JSON key: {key}")
        result[key] = value
    return result


def read_json(path):
    with path.open(encoding="utf-8") as source:
        return json.load(source, object_pairs_hook=unique_object)


def check_evidence(catalog, baseline):
    main_tests = set()
    for key, record in catalog.items():
        kind = record["kind"]
        require(record.get("summary") and record.get("limit"), f"{key}: missing outcome or coverage limit")
        if kind == "main_fixture_pass":
            require(record["revision"] == baseline, f"{key}: inconsistent baseline revision")
            names = record.get("tests", [])
            require(names or record.get("commands"), f"{key}: no executable case")
            for name in names:
                require(re.fullmatch(r"Test[A-Za-z0-9_]+", name), f"{key}: invalid Go test name")
                main_tests.add(name)
        elif kind == "review_pr_fixture_pass":
            require(PR.fullmatch(record["pr"]) and ISSUE.fullmatch(record["issue"]), f"{key}: missing PR or issue")
            require(SHA.fullmatch(record["revision"]) and record["revision"] != baseline, f"{key}: invalid PR head")
        else:
            require(kind == "historical_only", f"{key}: unknown evidence kind")
            source = (REPO / record["source"]).resolve()
            require(source.is_relative_to(REPO) and source.is_file(), f"{key}: missing archived report")
    return main_tests


def validate(archived, current):
    require(current["schema_version"] == 1 and current["release_status"] == "not_accepted",
            "this ledger cannot claim release acceptance")
    require(current["source_sequence"] == archived["source_sequence"], "different Page source sequence")
    require(SHA.fullmatch(current["baseline_revision"]), "unbound comparison revision")
    rows = [row for row in archived["requirements"] if row["kind"] == "requirement"]
    expected = {row["block_id"]: row["hash"] for row in rows}
    require(len(expected) == len(rows), "duplicate archived requirement ID")
    actual = current["requirements"]
    require(actual.keys() == expected.keys(), "current ledger is missing or adding source requirement IDs")
    evidence, blockers = current["evidence"], current["blockers"]
    used_evidence, used_blockers = set(), set()
    for block, row in actual.items():
        require(row["source_hash"] == expected[block], f"{block}: source change requires reassessment")
        scope = row["scope"]
        require(scope in SCOPES, f"{block}: unknown scope")
        require(scope == "applicable" or row.get("scope_note"), f"{block}: missing scope decision")
        require(set(row["evidence"]) <= evidence.keys(), f"{block}: unknown evidence ID")
        require(set(row["blockers"]) <= blockers.keys(), f"{block}: unknown blocker ID")
        require(bool(row["blockers"]) == (scope != "deferred"), f"{block}: incorrect blocker/scope mapping")
        if scope == "deferred":
            require(not row["evidence"], f"{block}: deferred work cannot claim implemented evidence")
        used_evidence.update(row["evidence"])
        used_blockers.update(row["blockers"])
    require(used_evidence == evidence.keys(), "unused evidence IDs")
    require(used_blockers == blockers.keys(), "unused blocker IDs")
    for key, blocker in blockers.items():
        require(blocker.get("remaining") and blocker.get("issue_numbers")
                and all(type(n) is int and n > 0 for n in blocker["issue_numbers"]),
                f"{key}: missing named follow-up and issue")
    return check_evidence(evidence, current["baseline_revision"])


def check_go_results(path, expected):
    with path.open(encoding="utf-8") as output:
        events = [json.loads(line) for line in output]
    require(not any("WARNING: DATA RACE" in row.get("Output", "") for row in events), "race found")
    passed = {row.get("Test") for row in events if row.get("Action") == "pass"}
    require(expected <= passed, f"baseline scenarios not observed passing: {sorted(expected - passed)}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go-race-json", type=Path, help="optional Go JSON output from the named main fixture tests")
    args = parser.parse_args()
    try:
        archived = read_json(DESIGN / "whirlwind-plan-acceptance.json")
        path = DESIGN / archived["current_evidence_file"]
        require(path.resolve().is_relative_to(DESIGN.resolve()), "current ledger must be in docs/design")
        current = read_json(path)
        expected = validate(archived, current)
        if args.go_race_json is not None:
            check_go_results(args.go_race_json, expected)
        print(f"Validated {len(current['requirements'])} requirement IDs, "
              f"{len(current['evidence'])} checks or records, and {len(current['blockers'])} named blockers.")
    except (KeyError, OSError, TypeError, ValueError) as error:
        print(f"Plan evidence invalid: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
