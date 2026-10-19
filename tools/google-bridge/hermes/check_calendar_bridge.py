#!/usr/bin/env python3
"""Daily Calendar gate. Install beside check_google_bridge.py and its private
google_bridge.json. Uses the same existing Hermes scheduler and owner MCP key.
No Google writes or background loop. Candidate data is read only by consented
Hermes tools after this content-free gate wakes the agent.
"""
from check_google_bridge import main

if __name__ == "__main__":
    raise SystemExit(main("calendar_sync"))
