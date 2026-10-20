import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from contextlib import redirect_stdout

spec = importlib.util.spec_from_file_location("gate", Path(__file__).parents[1] / "hermes/check_google_bridge.py")
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class Opener:
    def __init__(self, responses):
        self.responses = iter(responses)
        self.calls = []

    def open(self, request, timeout):
        self.calls.append(json.loads(request.data)["params"])
        return io.BytesIO(json.dumps({"result": {"structuredContent": next(self.responses)}}).encode())


class GateTests(unittest.TestCase):
    def test_private_config_and_url_boundary(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "google_bridge.json"
            path.write_text(json.dumps({"url": "http://127.0.0.1:8094/mcp", "key": "a" * 64}))
            path.chmod(0o600)
            self.assertEqual(gate.config(path)["key"], "a" * 64)
            path.chmod(0o644)
            with self.assertRaises(ValueError):
                gate.config(path)
            path.chmod(0o600)
            for url in ("http://remote.example/mcp", "https://me:secret@x/mcp", "https://x/other"):
                path.write_text(json.dumps({"url": url, "key": "a" * 64}))
                with self.assertRaises(ValueError):
                    gate.config(path)

    def test_idle_tick_calls_only_sync_and_skips_all_models(self):
        opener = Opener([{"triagePending": 0, "deliveryPending": 0, "messages": ["PRIVATE-BODY"]}])
        dispatched = []
        out = gate.tick({"url": "https://bridge.example/mcp", "key": "a"*64}, opener, dispatch=lambda *args: dispatched.append(args))
        self.assertEqual(out, {"wakeAgent": False})
        self.assertEqual(opener.calls, [{"name": "gmail_sync", "arguments": {}}])
        self.assertEqual(dispatched, [])

    def test_only_worker_starts_for_classification_or_delivery(self):
        for tool, service in (("gmail_sync", "gmail"), ("calendar_sync", "calendar")):
            for counts in ({"triagePending": 2}, {"deliveryPending": 2}):
                dispatched = []
                cfg = {"url": "https://bridge.example/mcp", "key": "a"*64}
                out = gate.tick(cfg, Opener([dict(counts, messages=["PRIVATE-BODY"])]), tool, dispatch=lambda *args: dispatched.append(args))
                self.assertEqual(out, {"wakeAgent": False})
                self.assertEqual(dispatched, [(cfg, service)])
                self.assertNotIn("PRIVATE", json.dumps(out))

    def test_calendar_and_mail_backlogs_are_bounded(self):
        for tool in ("gmail_sync", "calendar_sync"):
            opener = Opener([{"hasMore": True}] * 10)
            dispatched = []
            out = gate.tick({"url": "https://bridge.example/mcp", "key": "a"*64}, opener, tool, dispatch=lambda *args: dispatched.append(args))
            self.assertEqual(len(opener.calls), 10)
            self.assertEqual(len(dispatched), 1)
            self.assertEqual(out, {"wakeAgent": False})
            self.assertTrue(all(call["name"] == tool for call in opener.calls))

    def test_invalid_counts_or_provider_error_never_spawn_worker(self):
        for field, count in (("triagePending", 51), ("deliveryPending", -1), ("deliveryPending", True), ("deliveryPending", 201), ("deliveryPending", "2")):
            with self.assertRaises(ValueError):
                gate.tick({"url": "https://x/mcp", "key": "a"*64}, Opener([{field: count}]), dispatch=lambda *args: self.fail("invalid work dispatched"))
        class ErrorOpener:
            def open(self, request, timeout):
                return io.BytesIO(b'{"result":{"isError":true,"content":[{"text":"PRIVATE-BODY"}]}}')
        with self.assertRaisesRegex(ValueError, "^Sync failed$"):
            gate.tick({"url": "https://x/mcp", "key": "a"*64}, ErrorOpener())

    def test_daily_entry_point_uses_calendar_and_never_parent_agent(self):
        with patch.object(gate, "config", return_value={"url": "https://x/mcp", "key": "a"*64}), patch.object(gate, "tick", return_value={"wakeAgent": False}) as tick, redirect_stdout(io.StringIO()) as output:
            self.assertEqual(gate.main("calendar_sync"), 0)
            tick.assert_called_once_with({"url": "https://x/mcp", "key": "a"*64}, tool="calendar_sync")
            self.assertEqual(json.loads(output.getvalue()), {"wakeAgent": False})

    def test_spawn_passes_keys_only_on_stdin_and_uses_private_log(self):
        class Proc:
            class Input(io.StringIO):
                def close(self): self.saved = self.getvalue()
            stdin = Input()
        proc = Proc()
        with tempfile.TemporaryDirectory() as tmp, patch.dict(os.environ, {"HERMES_HOME": tmp}), patch.object(gate.subprocess, "Popen", return_value=proc) as spawn:
            gate.spawn_worker({"url": "https://bridge.example/mcp", "key": "SECRET-KEY"}, "gmail")
            args, kwargs = spawn.call_args
            self.assertNotIn("SECRET-KEY", repr(args))
            self.assertEqual(kwargs["stdout"], gate.subprocess.DEVNULL)
            self.assertEqual(json.loads(proc.stdin.saved)["bridge"]["key"], "SECRET-KEY")
            self.assertEqual((Path(tmp)/"google-bridge/gmail-worker.log").stat().st_mode & 0o777, 0o600)


if __name__ == "__main__":
    unittest.main()
