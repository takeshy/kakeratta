"""Run with: python3 -m unittest discover -s tests -p 'test_hermes_*.py'."""
import importlib.util
import io
import json
import os
import sqlite3
import subprocess
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

import yaml

SCRIPTS = Path(__file__).resolve().parents[1] / "frontend/public/integrations/hermes/kakeratta/scripts"
sys.path.insert(0, str(SCRIPTS))
import check_kakeratta_gate as gate
import direct_request_worker as worker


def future_plan_by():
    return (datetime.now(timezone.utc) + timedelta(minutes=15)).isoformat()


class JudgeIsolationTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.home = Path(self.tmp.name)
        self.wids = ["a" * 64, "b" * 64]
        self.headers = {"Authorization": "Bearer " + "j" * 64}
        self.cfg = {
            "model": {"default": "parent-model"},
            "delegation": {"model": "worker-model", "provider": "custom", "base_url": "https://provider.example/v1", "api_key": "${MODEL_KEY}", "reasoning": "high", "run_budget": 321},
            "mcp_servers": {"kakeratta": {"url": "https://host.example/api/mcp/users/parent"}, "private": {"url": "https://private.example"}},
            "kakeratta_worker": {"model": "worker-model", "toolsets": "terminal,web,kakeratta,files", "mcp_servers": ["private"], "file_roots": ["/home"]},
        }
        (self.home / "config.yaml").write_text(yaml.safe_dump(self.cfg))
        (self.home / ".env").write_text("MODEL_KEY=secret")
        skill = self.home / "skills/kakeratta/SKILL.md"
        skill.parent.mkdir(parents=True)
        skill.write_text("Claim pending asks and submit answers.")
        self.before = (self.home / "config.yaml").read_bytes()

    def url(self, wid):
        return "https://host.example/api/mcp/users/" + wid

    def test_direct_worker_scopes_state_tools_and_urls(self):
        seen = []
        def process_one(url, headers, request, cfg, home):
            generated = yaml.safe_load((cfg["worker_home"] / "config.yaml").read_text())
            self.assertEqual(set(generated["mcp_servers"]), {"kakeratta"})
            self.assertEqual(generated["mcp_servers"]["kakeratta"]["url"], url)
            self.assertEqual(generated["mcp_servers"]["kakeratta"]["headers"], self.headers)
            self.assertEqual(cfg["toolsets"], "web,kakeratta")
            self.assertEqual(cfg["model"], "worker-model")
            state = worker._state_path(home, request["id"])
            worker._atomic_write(state, json.dumps({"url": url}), mode=0o600)
            seen.append(state)
        with patch.object(worker, "process_one", side_effect=process_one):
            for wid in self.wids:
                worker.process(self.url(wid), self.headers, [{"id": "same-id", "status": "pending"}], home=self.home, workspace_id=wid)
        self.assertNotEqual(seen[0], seen[1])
        self.assertIn(self.wids[0], seen[0].read_text())
        self.assertIn(self.wids[1], seen[1].read_text())
        self.assertEqual((self.home / "config.yaml").read_bytes(), self.before)

    def test_normal_worker_preserves_configured_tools(self):
        with patch.object(worker, "process_one") as run:
            worker.process(self.url(self.wids[0]), self.headers, [{"id": "one", "status": "pending"}], home=self.home)
        cfg = run.call_args.args[3]
        self.assertEqual(cfg["toolsets"], "terminal,web,kakeratta,files")
        generated = yaml.safe_load((cfg["worker_home"] / "config.yaml").read_text())
        self.assertEqual(set(generated["mcp_servers"]), {"kakeratta", "files", "private"})

    def test_persona_cli_uses_only_target_workspace(self):
        calls = []
        def mcp_call(url, headers, name, arguments=None, timeout=None):
            calls.append((url, name, arguments))
            if name == "list_asks":
                return {"asks": [{"id": "ask-1", "status": "pending", "persona": "gourmet", "wake": "post"}]}
            if name == "claim_ask":
                return {"ask": {"id": "ask-1", "planBy": future_plan_by()}, "context": {
                    "persona": {"id": "gourmet", "name": "tabe", "tone": "丁寧", "domain": "食"},
                    "instructions": "食の相談に乗る", "rules": "規則", "turn": "※ あなたは名指しで呼ばれています。",
                    "wake": "post", "trigger": {"text": "おすすめは？", "rendered": "おすすめは？"},
                    "thread": [{"speaker": "user", "text": "おすすめは？", "rendered": "おすすめは？"}],
                    "tools": [{"name": "web_search", "use": "read"}], "limits": {"reads": 5, "searches": 5},
                }}
            if name == "submit_answer":
                return {"accepted": True, "closed": True}
            raise AssertionError(f"unexpected MCP call {name}")
        def run(cmd, prompt, env, runtime_cfg):
            home = Path(env["HERMES_HOME"])
            cfg = yaml.safe_load((home / "config.yaml").read_text())
            self.assertEqual(set(cfg["mcp_servers"]), {"kakeratta"})
            self.assertEqual(cfg["mcp_servers"]["kakeratta"]["url"], self.url(self.wids[0]))
            self.assertEqual(cfg["mcp_servers"]["kakeratta"]["headers"], self.headers)
            self.assertEqual(cfg["mcp_servers"]["kakeratta"]["tools"]["include"], ["read_for_ask"])
            self.assertEqual(cmd[cmd.index("-t") + 1], "kakeratta,web")
            self.assertFalse((home / "skills").exists())
            return type("Result", (), {"returncode": 0, "stdout": "[SOURCES: e_abc123]\n美味しいお店をご紹介します。\nsession_id: s1\n", "stderr": ""})()
        with patch.object(worker, "mcp_call", side_effect=mcp_call), \
             patch.object(worker, "session_usage", return_value={"api_call_count": 1, "input_tokens": 1000, "output_tokens": 200}), \
             patch.object(worker, "_run_persona_cli", side_effect=run):
            worker.process_persona_asks(self.url(self.wids[0]), self.headers, self.wids[0], home=self.home)
        self.assertEqual([c[1] for c in calls], ["list_asks", "claim_ask", "submit_answer"])
        self.assertEqual(calls[2][2]["answer"], {"text": "美味しいお店をご紹介します。", "sources": ["e_abc123"], "schedule": False, "unrelated": False})
        self.assertEqual((self.home / "config.yaml").read_bytes(), self.before)

    def test_persona_listening_turn_submits_without_model_call(self):
        calls = []
        def mcp_call(url, headers, name, arguments=None, timeout=None):
            calls.append((name, arguments))
            if name == "claim_ask":
                return {"ask": {"id": "ask-listen"}, "context": {
                    "persona": {"id": "gourmet", "name": "tabe"},
                    "wake": "listening", "turn": "※ **このターンは発言できません。**",
                }}
            if name == "submit_answer":
                return {"accepted": True, "closed": True}
            raise AssertionError(f"unexpected MCP call {name}")
        with patch.object(worker, "mcp_call", side_effect=mcp_call), \
             patch.object(worker, "_run_persona_cli") as run:
            worker.process_asks(self.url(self.wids[0]), self.headers,
                                [{"id": "ask-listen", "status": "pending"}], home=self.home)
            run.assert_not_called()
        self.assertEqual([c[0] for c in calls], ["claim_ask", "submit_answer"])
        self.assertEqual(calls[1][1]["answer"]["text"], "")

    def test_persona_failure_releases_ask_immediately(self):
        calls = []
        def mcp_call(url, headers, name, arguments=None, timeout=None):
            calls.append((name, arguments))
            if name == "claim_ask":
                return {"ask": {"id": "ask-fail", "planBy": future_plan_by()}, "context": {
                    "persona": {"id": "gourmet", "name": "tabe"},
                    "wake": "post", "turn": "※ あなたは名指しで呼ばれています。",
                }}
            if name == "release_ask":
                return {"released": True}
            raise AssertionError(f"unexpected MCP call {name}")
        with patch.object(worker, "mcp_call", side_effect=mcp_call), \
             patch.object(worker, "session_usage", return_value={"api_call_count": 1, "input_tokens": 500, "output_tokens": 0}), \
             patch.object(worker, "_run_persona_cli", return_value=type("Result", (), {"returncode": 1, "stdout": "", "stderr": "provider error"})()):
            worker.process_asks(self.url(self.wids[0]), self.headers,
                                [{"id": "ask-fail", "status": "pending"}], home=self.home)
        self.assertEqual([c[0] for c in calls], ["claim_ask", "release_ask"])

    def test_legacy_super_persona_uses_super_vl_and_keeps_ultra_separate(self):
        self.cfg["model"] = {"default": worker.LEGACY_PARENT_SUPER,
                             "provider": "custom", "base_url": "https://api.tokenfactory.nebius.com/v1",
                             "api_key": "${MODEL_KEY}"}
        self.cfg.pop("kakeratta_worker")
        (self.home / "config.yaml").write_text(yaml.safe_dump(self.cfg))
        def mcp_call(url, headers, name, arguments=None, timeout=None):
            if name == "claim_ask":
                return {"ask": {"id": "ask-vl", "planBy": future_plan_by()}, "context": {"persona": {"id": "gourmet", "name": "tabe"}, "wake": "post"}}
            return {"accepted": True, "closed": True}
        def run(cmd, prompt, env, runtime_cfg):
            generated = yaml.safe_load((Path(env["HERMES_HOME"]) / "config.yaml").read_text())
            self.assertEqual(generated["model"]["default"], worker.PARENT_SUPER_VL)
            self.assertEqual(generated["model"]["api_key"], "${MODEL_KEY}")
            self.assertEqual(generated["mcp_servers"]["kakeratta"]["tools"]["include"], ["read_for_ask"])
            return type("Result", (), {"returncode": 0, "stdout": "回答です\nsession_id: s1\n", "stderr": ""})()
        with patch.object(worker, "mcp_call", side_effect=mcp_call), \
             patch.object(worker, "session_usage", return_value={"api_call_count": 1, "input_tokens": 500, "output_tokens": 100}), \
             patch.object(worker, "_run_persona_cli", side_effect=run):
            worker.process_persona_asks(self.url(self.wids[0]), self.headers, self.wids[0],
                                        home=self.home, asks=[{"id": "ask-vl", "status": "pending"}])
        self.assertEqual(worker.model_config(self.cfg)["model"], "worker-model")
        self.assertEqual(yaml.safe_load((self.home / "config.yaml").read_text())["model"]["default"], worker.LEGACY_PARENT_SUPER)
        self.cfg["model"]["default"] = "custom-model"
        self.assertEqual(worker.persona_model_config(self.cfg)["default"], "custom-model")
        self.cfg["model"]["default"] = worker.LEGACY_PARENT_SUPER
        self.cfg["model"]["base_url"] = "https://other.example/v1"
        self.assertEqual(worker.persona_model_config(self.cfg)["default"], worker.LEGACY_PARENT_SUPER)

    def test_idle_and_failed_workspace_never_invoke_models(self):
        with patch.dict(os.environ, {"HERMES_HOME": str(self.home), "JUDGE_MCP_KEY": "j" * 64, "JUDGE_WORKSPACES_URL": "https://host.example/internal/judge-workspaces"}), \
             patch.object(gate, "discover_judges", return_value={wid: self.url(wid) for wid in self.wids}), \
             patch.object(gate, "call_tool", side_effect=[RuntimeError("offline"), {"asks": [], "requests": []}]) as poll, \
             patch.object(gate, "update_integration"), \
             patch.object(worker, "spawn_persona_asks") as asks, \
             patch.object(worker, "spawn_judge_requests") as requests:
            gate.judge_tick()
            self.assertEqual(poll.call_count, 2)
            asks.assert_not_called()
            requests.assert_not_called()

    def test_gate_dispatches_each_workspace_without_waiting_for_inference(self):
        with patch.dict(os.environ, {"HERMES_HOME": str(self.home), "JUDGE_MCP_KEY": "j" * 64, "JUDGE_WORKSPACES_URL": "https://host.example/internal/judge-workspaces"}), \
             patch.object(gate, "discover_judges", return_value={wid: self.url(wid) for wid in self.wids}), \
             patch.object(gate, "call_tool", return_value={"asks": [{"id": "ask", "status": "pending"}], "requests": [{"id": "req", "status": "pending"}]}), \
             patch.object(gate, "update_integration"), \
             patch.object(worker, "spawn_persona_asks") as asks, \
             patch.object(worker, "spawn_judge_requests") as requests:
            gate.judge_tick()
            self.assertEqual([c.args[2] for c in asks.call_args_list], self.wids)
            self.assertEqual([c.args[2] for c in requests.call_args_list], self.wids)

    def test_discovery_refuses_cross_origin_and_redirects(self):
        response = io.BytesIO(json.dumps({"workspaces": [{"workspaceId": self.wids[0], "mcpUrl": "https://evil.example/mcp"}]}).encode())
        with patch.object(gate.urllib.request, "build_opener") as opener:
            opener.return_value.open.return_value = response
            with self.assertRaises(ValueError):
                gate.discover_judges("https://host.example/internal/judge-workspaces", self.headers)
        with self.assertRaises(ValueError):
            gate.NoRedirect().redirect_request(None, None, 302, "", {}, "https://evil.example")

    def test_workspace_locks_and_paths_do_not_collide(self):
        a, b = [worker._judge_root(self.home, wid) for wid in self.wids]
        first = worker.acquire_lock(a)
        try:
            self.assertIsNone(worker.acquire_lock(a))
            other = worker.acquire_lock(b)
            self.assertIsNotNone(other)
            other.close()
        finally:
            first.close()
        with self.assertRaises(ValueError):
            worker._judge_root(self.home, "../other")


class PersonaBudgetTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.home = Path(self.tmp.name)
        self.cfg = worker.persona_run_config({})
        self.cfg.update(worker_home=self.home, secrets=[])
        self.cfg["meter"] = worker.Meter(self.cfg, self.home, "ask-budget")

    def test_completed_over_budget_answer_is_submitted_and_retry_does_not_regenerate(self):
        for usage in ({"api_call_count": 6, "input_tokens": 260000, "output_tokens": 1000},
                      {"api_call_count": 8, "input_tokens": 100, "output_tokens": 10}, None):
            with self.subTest(usage=usage), tempfile.TemporaryDirectory() as td:
                calls = []
                def mcp(url, headers, name, arguments=None, timeout=None):
                    calls.append(name)
                    if name == "claim_ask":
                        return {"ask": {"planBy": future_plan_by()}, "context": {"wake": "post"}}
                    if calls.count("submit_answer") == 1:
                        raise OSError("submission transport failure")
                    self.assertEqual(arguments["answer"]["text"], "完成した回答")
                    return {"accepted": True}
                result = subprocess.CompletedProcess([], 0, "完成した回答\nsession_id: s1", "")
                with patch.object(worker, "mcp_call", side_effect=mcp), \
                     patch.object(worker, "session_usage", return_value=usage), \
                     patch.object(worker, "_run_persona_cli", return_value=result) as run:
                    worker.process_one_ask("url", {}, {"id": "a", "status": "pending"}, self.cfg, Path(td))
                    worker.process_one_ask("url", {}, {"id": "a", "status": "claimed"}, self.cfg, Path(td))
                    self.assertEqual(run.call_count, 1)
                self.assertEqual(calls, ["claim_ask", "submit_answer", "submit_answer"])

    def test_deadline_caps_budget_and_does_not_mutate_next_ask(self):
        self.cfg["run_budget"] = 1200
        before = time.monotonic()
        bounded = worker._persona_deadline_config(self.cfg, {"ask": {"planBy": future_plan_by()}})
        self.assertGreaterEqual(bounded["run_budget"], 868)
        self.assertLessEqual(bounded["run_budget"], 870)
        self.assertLessEqual(bounded["persona_deadline"], before + 871)
        self.assertEqual(self.cfg["run_budget"], 1200)
        self.cfg["run_budget"] = 10
        self.assertEqual(worker._persona_deadline_config(self.cfg, {"ask": {"planBy": future_plan_by()}})["run_budget"], 10)

    def test_expired_missing_and_invalid_deadlines_release_without_inference(self):
        expired = (datetime.now(timezone.utc) + timedelta(seconds=20)).isoformat()
        for deadline in (expired, None, "invalid", "2026-10-16T10:00:00"):
            with self.subTest(deadline=deadline), tempfile.TemporaryDirectory() as td:
                calls = []
                def mcp(url, headers, name, arguments=None, timeout=None):
                    calls.append(name)
                    return {"ask": {"planBy": deadline}, "context": {"wake": "post"}}
                with patch.object(worker, "mcp_call", side_effect=mcp), \
                     patch.object(worker, "_run_persona_cli") as run:
                    worker.process_one_ask("url", {}, {"id": "a", "status": "pending"}, self.cfg, Path(td))
                    run.assert_not_called()
                self.assertEqual(calls, ["claim_ask", "release_ask"])

    def run_cli(self, script, budget=5):
        self.cfg["persona_deadline"] = time.monotonic() + budget
        real_popen = subprocess.Popen
        children = []
        def popen(*args, **kwargs):
            proc = real_popen(*args, **kwargs)
            children.append(proc)
            return proc
        try:
            with patch.object(worker.subprocess, "Popen", side_effect=popen), \
                 patch.object(worker, "PERSONA_POLL_SECONDS", 0.05):
                return worker._run_persona_cli([sys.executable, "-c", script], "test prompt", dict(os.environ), self.cfg)
        finally:
            for proc in children:
                self.assertIsNotNone(proc.poll(), "CLI must be reaped")

    def test_live_sqlite_budget_stops_unfinished_cli_and_accounts_usage(self):
        # Write a previous session that must not count against this run.
        with sqlite3.connect(self.home / "state.db") as con:
            con.execute("CREATE TABLE sessions (id TEXT, api_call_count INTEGER, input_tokens INTEGER, output_tokens INTEGER)")
            con.execute("INSERT INTO sessions VALUES ('old', 100, 900000, 0)")
        script = """
import sqlite3, time, sys
assert sys.stdin.read() == 'test prompt'
with sqlite3.connect('state.db') as con:
    con.execute("INSERT INTO sessions VALUES ('new', 3, 260000, 1000)")
print('unfinished output', flush=True)
time.sleep(30)
"""
        with self.assertRaisesRegex(worker.BudgetExceeded, "261000 tokens"):
            self.run_cli(script)
        usage = json.loads(worker._usage_path(self.home, "ask-budget").read_text())
        self.assertEqual(usage["tokens"], 261000)
        self.assertEqual(usage["runs"], 1)

    def test_deadline_stops_cli_and_its_tool_process(self):
        script = """
import subprocess, sys, time
subprocess.Popen([sys.executable, '-c', "import time; from pathlib import Path; time.sleep(0.7); Path('tool-ran').touch()"])
print('unfinished output', flush=True)
time.sleep(30)
"""
        with self.assertRaisesRegex(worker.BudgetExceeded, "deadline"):
            self.run_cli(script, budget=0.2)
        time.sleep(0.8)
        self.assertFalse((self.home / "tool-ran").exists())

    def test_completed_cli_keeps_output_and_ignores_old_session_usage(self):
        with sqlite3.connect(self.home / "state.db") as con:
            con.execute("CREATE TABLE sessions (id TEXT, api_call_count INTEGER, input_tokens INTEGER, output_tokens INTEGER)")
            con.execute("INSERT INTO sessions VALUES ('old', 100, 900000, 0)")
        result = self.run_cli("import sys, time; assert sys.stdin.read() == 'test prompt'; time.sleep(0.15); print('complete answer')")
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout.strip(), "complete answer")

    def test_live_limits_include_previous_runs_without_accumulating_each_poll(self):
        meter = self.cfg["meter"]
        meter.record("persona", "previous", {"api_call_count": 5, "input_tokens": 200000})
        meter.check({"api_call_count": 2, "input_tokens": 40000})
        meter.check({"api_call_count": 2, "input_tokens": 40000})
        self.assertEqual(meter.totals["tokens"], 200000)
        with self.assertRaisesRegex(worker.BudgetExceeded, "8 model calls"):
            meter.check({"api_call_count": 3, "input_tokens": 100})
        with self.assertRaisesRegex(worker.BudgetExceeded, "250000 tokens"):
            meter.check({"api_call_count": 1, "input_tokens": 50000})

    def test_unreadable_usage_database_prevents_inference(self):
        (self.home / "state.db").write_text("broken sqlite database")
        self.cfg["persona_deadline"] = time.monotonic() + 5
        with patch.object(worker.subprocess, "Popen") as popen:
            with self.assertRaisesRegex(worker.BudgetExceeded, "unreadable"):
                worker._run_persona_cli([], "prompt", {}, self.cfg)
            popen.assert_not_called()

    def test_direct_request_budget_behavior_is_preserved(self):
        result = subprocess.CompletedProcess([], 0, "direct reply\nsession_id: s1", "")
        with patch.object(worker.subprocess, "run", return_value=result), \
             patch.object(worker, "session_usage", return_value={"api_call_count": 8, "input_tokens": 100}), \
             patch.object(worker, "_run_persona_cli") as persona:
            with self.assertRaises(worker.BudgetExceeded):
                worker.hermes_run(self.cfg, "prompt", "web", 6, "agent")
            persona.assert_not_called()


if __name__ == "__main__":
    unittest.main()
