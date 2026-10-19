import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("gate", Path(__file__).parents[1] / "hermes" / "check_google_bridge.py")
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


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

    def test_tick_calls_only_sync_and_never_outputs_email(self):
        class Opener:
            def open(self, request, timeout):
                self.body = json.loads(request.data)
                return io.BytesIO(json.dumps({"result": {"structuredContent": {"importedIds": ["PRIVATE-ID"]}}}).encode())
        opener = Opener()
        out = gate.tick({"url": "http://127.0.0.1:8094/mcp", "key": "a" * 64}, opener)
        self.assertEqual(out, {"wakeAgent": False})
        self.assertEqual(opener.body["params"], {"name": "gmail_sync", "arguments": {}})

    def test_provider_error_is_not_forwarded(self):
        class Opener:
            def open(self, request, timeout):
                return io.BytesIO(b'{"result":{"isError":true,"content":[{"text":"PRIVATE-BODY"}]}}')
        with self.assertRaisesRegex(ValueError, "^Sync failed$"):
            gate.tick({"url": "https://x/mcp", "key": "a" * 64}, Opener())

class TriageWakeTests(unittest.TestCase):
    def test_only_pending_classification_wakes_the_agent(self):
        class Opener:
            def open(self, request, timeout):
                return io.BytesIO(b'{"result":{"structuredContent":{"triagePending":2,"messages":["PRIVATE-BODY"]}}}')
        out = gate.tick({"url": "https://x/mcp", "key": "a" * 64}, Opener())
        self.assertTrue(out["wakeAgent"])
        self.assertEqual(out["context"]["triagePending"], 2)
        self.assertNotIn("PRIVATE-BODY", json.dumps(out))


class CalendarGateTests(unittest.TestCase):
    def test_calendar_gate_drains_pages_and_only_wakes_for_classification(self):
        class Opener:
            count = 0
            def open(self, request, timeout):
                self.count += 1
                args = json.loads(request.data)
                assert args["params"]["name"] == "calendar_sync"
                payload = {"hasMore": self.count == 1, "triagePending": 0 if self.count == 1 else 2, "events": ["PRIVATE EVENT"]}
                return io.BytesIO(json.dumps({"result": {"structuredContent": payload}}).encode())
        opener = Opener()
        out = gate.tick({"url": "https://x/mcp", "key": "a" * 64}, opener, "calendar_sync")
        self.assertEqual(opener.count, 2)
        self.assertTrue(out["wakeAgent"])
        self.assertIn("calendar_triage_context", out["context"]["instruction"])
        self.assertNotIn("PRIVATE EVENT", json.dumps(out))

if __name__ == "__main__":
    unittest.main()
