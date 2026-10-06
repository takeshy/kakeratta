#!/usr/bin/env python3
"""Run Terraform with remote state and private, versioned GCS variable files."""

import os
from pathlib import Path
import subprocess
import sys
import tempfile

BUCKET = "takeshy-work-kakeratta-terraform"
ROOT = Path(__file__).resolve().parent
MODULES = {
    "app": (ROOT, ["terraform.tfvars", "shared-vm.auto.tfvars"]),
    "shared-vm": (ROOT / "shared-vm", ["terraform.tfvars"]),
}


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def main():
    if len(sys.argv) < 3 or sys.argv[1] not in MODULES:
        raise SystemExit("Usage: python3 infra/terraform.py {app|shared-vm} {init|plan|apply|output|upload-vars|...} [args...]")
    module, command, *args = sys.argv[1:]
    directory, names = MODULES[module]
    if command == "fetch-vars":
        if args not in ([], ["--force"]):
            raise SystemExit("fetch-vars accepts only --force")
        if not args and any((directory / name).exists() for name in names):
            raise SystemExit("Local variable files exist; use --force to replace them with GCS copies")
        private = directory / ".terraform"
        private.mkdir(mode=0o700, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix="tfvars-", dir=private) as temp:
            for name in names:
                target = Path(temp) / name
                run("gcloud", "storage", "cp", f"gs://{BUCKET}/vars/{module}/{name}", str(target))
                target.chmod(0o600)
            for name in names:
                (Path(temp) / name).replace(directory / name)
        return
    if command == "upload-vars":
        if args:
            raise SystemExit("upload-vars takes no additional arguments")
        # Check all files before publishing any of them. Object versioning
        # retains earlier revisions when the operator updates configuration.
        for name in names:
            if not (directory / name).is_file():
                raise SystemExit(f"Missing local variable file: {directory / name}")
        for name in names:
            run("gcloud", "storage", "cp", str(directory / name), f"gs://{BUCKET}/vars/{module}/{name}")
        return

    env = dict(os.environ)
    if not env.get("GOOGLE_OAUTH_ACCESS_TOKEN") and not any(
        env.get(key) for key in ("GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CREDENTIALS", "GOOGLE_BACKEND_CREDENTIALS")
    ):
        # Environment-only credentials keep tokens out of Terraform plans,
        # backend configuration, process arguments and command output.
        env["GOOGLE_OAUTH_ACCESS_TOKEN"] = subprocess.check_output(
            ["gcloud", "auth", "print-access-token"], text=True
        ).strip()

    with tempfile.TemporaryDirectory(prefix="kakeratta-tfvars-") as temp:
        variable_args = []
        saved_plan = command == "apply" and any(
            not arg.startswith("-") and (directory / arg).is_file() for arg in args
        )
        if command == "plan" or (command == "apply" and not saved_plan):
            for name in names:
                target = Path(temp) / name
                run("gcloud", "storage", "cp", f"gs://{BUCKET}/vars/{module}/{name}", str(target))
                target.chmod(0o600)
                variable_args.append(f"-var-file={target}")
        run("terraform", f"-chdir={directory}", command, *variable_args, *args, env=env)


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError as error:
        raise SystemExit(error.returncode)
