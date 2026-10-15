#!/usr/bin/env python3
"""Migrate the Token Factory parent and selected Cron pins to Super VL.

Run inside the Hermes sandbox with its Python environment. Preview by default;
--apply creates a private config backup and edits Cron through the Hermes CLI.
Delegation and direct-request worker settings are never changed.
"""

import argparse
import copy
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

import yaml

SUPER_VL = "nvidia/Nemotron-3.5-Super-VL"
TOKEN_FACTORY = "https://api.tokenfactory.nebius.com/v1"


def migrated_config(config):
    result = copy.deepcopy(config)
    model = result.get("model")
    if not isinstance(model, dict):
        raise ValueError("model must be a mapping")
    if model.get("provider") != "custom" or str(model.get("base_url", "")).rstrip("/") != TOKEN_FACTORY:
        raise ValueError("expected the existing custom Token Factory provider")
    model["default"] = SUPER_VL
    # A global Cron override takes precedence over the parent's default.
    cron = result.get("cron")
    if isinstance(cron, dict) and str(cron.get("model", "")).lower() in ("nvidia/nemotron-3-super-120b-a12b", SUPER_VL.lower()):
        cron["model"] = SUPER_VL
        cron["model_provider"] = "custom"
    return result


def migrate(home, job_ids, hermes_bin, apply=False):
    path = home / "config.yaml"
    original = path.read_bytes()
    config = yaml.safe_load(original)
    updated = migrated_config(config)
    if not job_ids or any(not job or job.startswith("-") for job in job_ids):
        raise ValueError("at least one explicit kakeratta Cron job ID is required")
    plan = {"parentModel": SUPER_VL, "provider": "custom", "cronJobs": job_ids, "applied": False}
    if not apply:
        return plan
    binary = shutil.which(hermes_bin)
    if not binary:
        raise ValueError("Hermes CLI not found")
    # Use the CLI's own job-store locking; never rewrite jobs.json ourselves.
    # Re-running after partial failure safely applies the same pins again.
    for job_id in job_ids:
        proc = subprocess.run([binary, "cron", "edit", job_id, "--model", SUPER_VL,
                               "--provider", "custom"],
                              env=dict(os.environ, HERMES_HOME=str(home)),
                              capture_output=True, text=True, timeout=30)
        if proc.returncode:
            raise RuntimeError("Cron model update failed; parent config unchanged; rerun after checking the job ID")
    if path.read_bytes() != original:
        raise RuntimeError("config changed during migration; retry")
    if updated != config:
        backup_fd, backup = tempfile.mkstemp(prefix="config.before-super-vl-", suffix=".yaml", dir=home)
        with os.fdopen(backup_fd, "wb") as out:
            out.write(original)
        temp_fd, temp_path = tempfile.mkstemp(prefix="config.super-vl-", suffix=".yaml", dir=home)
        try:
            with os.fdopen(temp_fd, "w") as out:
                yaml.safe_dump(updated, out, allow_unicode=True, sort_keys=False)
            os.replace(temp_path, path)
        finally:
            if os.path.exists(temp_path):
                os.unlink(temp_path)
        plan["backup"] = Path(backup).name
    plan["applied"] = True
    return plan


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--home", type=Path, default=Path(os.environ.get("HERMES_HOME", "~/.hermes")).expanduser())
    parser.add_argument("--job-id", action="append", required=True)
    parser.add_argument("--hermes-bin", default="hermes")
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    try:
        print(json.dumps(migrate(args.home.resolve(), args.job_id, args.hermes_bin, args.apply)))
    except Exception:
        # CLI/config errors can contain credentials; do not echo their text.
        parser.exit(1, "Super VL migration failed; inspect configuration and Cron IDs locally, then retry.\n")


if __name__ == "__main__":
    main()
