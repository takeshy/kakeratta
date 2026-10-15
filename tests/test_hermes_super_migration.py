"""Model migration checks with a fake local Hermes CLI; no model API calls."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import yaml

SCRIPT = Path(__file__).resolve().parents[1] / "scripts/nemoclaw/bin/nemoclaw-hermes-upgrade-super.py"
spec = importlib.util.spec_from_file_location("upgrade_super", SCRIPT)
upgrade = importlib.util.module_from_spec(spec)
spec.loader.exec_module(upgrade)


class MigrationTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.home = Path(self.tmp.name)
        self.config = {
            "model": {"default": "nvidia/nemotron-3-super-120b-a12b", "provider": "custom", "base_url": upgrade.TOKEN_FACTORY, "api_key": "${TOKENFACTORY_API_KEY}"},
            "cron": {"model": "nvidia/nemotron-3-super-120b-a12b", "model_provider": "custom", "max_runs": 2},
            "delegation": {"model": "nvidia/Nemotron-3-Ultra-550b-a55b", "reasoning_effort": "high"},
            "kakeratta_worker": {"model": "nvidia/Nemotron-3-Ultra-550b-a55b", "run_budget": 1200},
            "mcp_servers": {"kakeratta": {"headers": {"Authorization": "Bearer private-test-key"}}},
        }
        (self.home / "config.yaml").write_text(yaml.safe_dump(self.config))
        self.original = (self.home / "config.yaml").read_bytes()
        self.cli = self.home / "hermes"
        self.cli.write_text('''#!/usr/bin/env python3
import json, os, pathlib, sys
path = pathlib.Path(os.environ["HERMES_HOME"])
with (path / "pins.jsonl").open("a") as out:
 out.write(json.dumps(sys.argv[1:]) + "\\n")
sys.exit(0)
''')
        self.cli.chmod(0o700)

    def test_preview_has_no_side_effects_or_credentials(self):
        plan = upgrade.migrate(self.home, ["job-one"], str(self.cli))
        self.assertFalse(plan["applied"])
        self.assertNotIn("private-test-key", json.dumps(plan))
        self.assertEqual((self.home / "config.yaml").read_bytes(), self.original)
        self.assertFalse((self.home / "pins.jsonl").exists())

    def test_applies_parent_and_selected_pins_and_preserves_worker(self):
        plan = upgrade.migrate(self.home, ["job-one", "job-two"], str(self.cli), apply=True)
        current = yaml.safe_load((self.home / "config.yaml").read_text())
        self.assertEqual(current["model"]["default"], upgrade.SUPER_VL)
        self.assertEqual(current["cron"]["model"], upgrade.SUPER_VL)
        self.assertEqual(current["cron"]["model_provider"], "custom")
        for section in ("delegation", "kakeratta_worker", "mcp_servers"):
            self.assertEqual(current[section], self.config[section])
        self.assertEqual((self.home / plan["backup"]).read_bytes(), self.original)
        self.assertEqual((self.home / plan["backup"]).stat().st_mode & 0o777, 0o600)
        self.assertEqual((self.home / "config.yaml").stat().st_mode & 0o777, 0o600)
        pins = [json.loads(line) for line in (self.home / "pins.jsonl").read_text().splitlines()]
        self.assertEqual([pin[2] for pin in pins], ["job-one", "job-two"])
        self.assertTrue(all(pin[4] == upgrade.SUPER_VL and pin[6] == "custom" for pin in pins))
        again = upgrade.migrate(self.home, ["job-one"], str(self.cli), apply=True)
        self.assertNotIn("backup", again)

    def test_failed_cli_keeps_original_config(self):
        self.cli.write_text("#!/bin/sh\nexit 1\n")
        with self.assertRaises(RuntimeError):
            upgrade.migrate(self.home, ["missing-job"], str(self.cli), apply=True)
        self.assertEqual((self.home / "config.yaml").read_bytes(), self.original)
        self.assertEqual(list(self.home.glob("config.before-*")), [])

    def test_foreign_provider_refused_and_other_cron_model_preserved(self):
        other = copy.deepcopy(self.config)
        other["model"]["base_url"] = "https://other.example/v1"
        with self.assertRaises(ValueError):
            upgrade.migrated_config(other)
        other = copy.deepcopy(self.config)
        other["cron"] = {"model": "other-model", "model_provider": "other-provider"}
        self.assertEqual(upgrade.migrated_config(other)["cron"], other["cron"])


if __name__ == "__main__":
    unittest.main()
