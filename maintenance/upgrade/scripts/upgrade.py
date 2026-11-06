#!/usr/bin/env python3
"""Verify and apply SHA256-pinned Woodpecker source patches in a new Git worktree.

Never contacts a live Woodpecker service, accesses secrets, pushes branches,
changes GitHub settings or deploys binaries. Requires a locally fetched,
immutable upstream tag and an explicit SHA.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any

UPGRADE_ROOT = Path(__file__).resolve().parents[1]
REPO_ROOT = UPGRADE_ROOT.parents[1]
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
ID_RE = re.compile(r"^[a-z][a-z0-9-]*$")


class UpgradeError(ValueError):
    pass


def git(*args: str, cwd: Path = REPO_ROOT, check: bool = True) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(["git", *args], cwd=cwd, text=True, capture_output=True, check=False)
    if check and result.returncode:
        raise UpgradeError("git " + " ".join(args) + ": " + result.stderr.strip()[:1000])
    return result


def load_manifest(path: Path = UPGRADE_ROOT / "modules.json") -> dict[str, Any]:
    data = json.loads(path.read_text(encoding="utf-8"))
    if data.get("schema_version") != 1 or not isinstance(data.get("modules"), list):
        raise UpgradeError("unsupported modules schema")
    if not SHA_RE.fullmatch(str(data.get("baseline_sha", ""))):
        raise UpgradeError("invalid baseline SHA")
    if not SHA_RE.fullmatch(str(data.get("source_sha", ""))):
        raise UpgradeError("invalid source SHA")
    seen: set[str] = set()
    for module in data["modules"]:
        mid = module.get("id")
        if not isinstance(mid, str) or not ID_RE.fullmatch(mid) or mid in seen:
            raise UpgradeError("invalid or duplicate module id")
        seen.add(mid)
        rel = module.get("patch_path")
        if not isinstance(rel, str) or rel != f"maintenance/upgrade/patches/{mid}.patch":
            raise UpgradeError("module patch path is not canonical")
        patch = REPO_ROOT / rel
        if patch.is_symlink() or not patch.is_file() or not patch.resolve().is_relative_to((UPGRADE_ROOT / "patches").resolve()):
            raise UpgradeError("patch file missing or unsafe")
        expected = module.get("patch_sha256")
        if not isinstance(expected, str) or not re.fullmatch(r"[0-9a-f]{64}", expected):
            raise UpgradeError("invalid patch SHA256")
        actual = hashlib.sha256(patch.read_bytes()).hexdigest()
        if actual != expected:
            raise UpgradeError(f"module checksum mismatch: {mid}")
        if patch.stat().st_size != module.get("size_bytes"):
            raise UpgradeError(f"module size mismatch: {mid}")
    return data


def verify_target(ref: str, expected_sha: str) -> str:
    if not SHA_RE.fullmatch(expected_sha):
        raise UpgradeError("expected upstream SHA must contain exactly 40 hex characters")
    if not ref or ref.startswith("-") or ".." in ref or any(ch.isspace() for ch in ref):
        raise UpgradeError("unsafe upstream ref")
    observed = git("rev-parse", "--verify", f"{ref}^{{commit}}").stdout.strip()
    if observed != expected_sha:
        raise UpgradeError(f"upstream ref mismatch: expected {expected_sha}, observed {observed}")
    return observed


def prepare_output(path: Path) -> Path:
    out = path.expanduser().resolve()
    if out.exists():
        raise UpgradeError("output must not already exist: " + str(out))
    if out == REPO_ROOT or out.is_relative_to(REPO_ROOT):
        raise UpgradeError("output cannot be inside source repository")
    if out == Path("/") or len(out.parts) < 3:
        raise UpgradeError("unsafe output path")
    return out


def run(command: str, ref: str | None, expected_sha: str | None, output: Path | None) -> dict[str, Any]:
    manifest = load_manifest()
    result: dict[str, Any] = {
        "schema_version": 1,
        "command": command,
        "manifest_sha256": hashlib.sha256(
            (UPGRADE_ROOT / "modules.json").read_bytes()
        ).hexdigest(),
        "source_sha": manifest["source_sha"],
        "module_count": len(manifest["modules"]),
        "status": "verified",
        "modules": [{"id": m["id"], "sha256": m["patch_sha256"]} for m in manifest["modules"]],
        "live_changes": 0,
        "secret_access": 0,
    }
    if command == "verify":
        return result
    if ref is None or expected_sha is None:
        raise UpgradeError("target ref and expected SHA are both required")
    result["upstream_sha"] = verify_target(ref, expected_sha)
    result["upstream_ref"] = ref
    if command == "plan":
        result["status"] = "ready-for-explicit-source-only-apply"
        return result
    if command != "apply" or output is None:
        raise UpgradeError("apply requires an explicit unused output directory")
    out = prepare_output(output)
    git("worktree", "add", "--detach", str(out), ref)
    result["status"] = "applying"
    result["modules"] = []
    for module in manifest["modules"]:
        patch = REPO_ROOT / module["patch_path"]
        check = git("apply", "--check", str(patch), cwd=out, check=False)
        if check.returncode:
            result["status"] = "conflict-needs-reviewed-port"
            result["modules"].append({
                "id": module["id"],
                "status": "conflict",
                "error": check.stderr.strip()[:2000],
            })
            break
        git("apply", str(patch), cwd=out)
        result["modules"].append({"id": module["id"], "status": "applied"})
    if result["status"] == "applying":
        whitespace = git("diff", "--check", cwd=out, check=False)
        result["status"] = "source-applied" if whitespace.returncode == 0 else "whitespace-review-required"
        if whitespace.returncode:
            result["diff_check"] = whitespace.stderr.strip()[:1000]
    result["worktree"] = str(out)
    (out / "upgrade-report.json").write_text(
        json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("verify", "plan", "apply"))
    parser.add_argument("--target-ref")
    parser.add_argument("--expected-sha")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    try:
        data = run(args.command, args.target_ref, args.expected_sha, args.output)
    except (UpgradeError, OSError, json.JSONDecodeError) as exc:
        print(json.dumps({"status": "blocked", "reason": str(exc), "live_changes": 0}, ensure_ascii=False))
        return 2
    print(json.dumps(data, ensure_ascii=False, indent=2))
    return 0 if data["status"] in {"verified", "ready-for-explicit-source-only-apply", "source-applied"} else 3


if __name__ == "__main__":
    sys.exit(main())
