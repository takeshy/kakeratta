#!/usr/bin/env python3
# ==============================================================================
# NemoClaw / OpenShell Sandbox Reconciler
# ==============================================================================
# OpenShell Gateway DB (openshell.db) 内の hermes-sandbox の状態を検査し、
# Phase: Error (enum 3) や異常な exit_code が記録されてデッドロックしている場合、
# 安全に Phase: Ready (enum 2) に再同期して Gateway デーモンを再起動します。
# ==============================================================================

import sys
import os
import sqlite3
import shutil
from datetime import datetime

DEFAULT_DB_PATH = os.path.expanduser("~/.local/state/nemoclaw/openshell-docker-gateway/openshell.db")
DB_PATH = os.environ.get("OPENSHELL_DB_PATH", DEFAULT_DB_PATH)
SANDBOX_NAME = os.environ.get("SANDBOX_NAME", "hermes-sandbox")

def encode_varint(val):
    b = bytearray()
    while val > 0x7F:
        b.append((val & 0x7F) | 0x80)
        val >>= 7
    b.append(val & 0x7F)
    return bytes(b)

def encode_tag(field_num, wire_type):
    return encode_varint((field_num << 3) | wire_type)

def encode_len_delim(field_num, data):
    return encode_tag(field_num, 2) + encode_varint(len(data)) + data

def parse_protobuf(data):
    i = 0
    items = []
    while i < len(data):
        start_pos = i
        shift = 0; key = 0
        while True:
            if i >= len(data): return items
            b = data[i]; i += 1
            key |= (b & 0x7F) << shift
            shift += 7
            if not (b & 0x80): break
        fnum = key >> 3
        wtype = key & 7
        if wtype == 0:
            shift = 0; val = 0
            while True:
                b = data[i]; i += 1
                val |= (b & 0x7F) << shift
                shift += 7
                if not (b & 0x80): break
            items.append((fnum, wtype, val, start_pos, i))
        elif wtype == 2:
            shift = 0; length = 0
            while True:
                b = data[i]; i += 1
                length |= (b & 0x7F) << shift
                shift += 7
                if not (b & 0x80): break
            val = data[i:i+length]
            i += length
            items.append((fnum, wtype, val, start_pos, i))
        else:
            items.append((fnum, wtype, 'unknown', start_pos, i))
    return items

def reconcile():
    if not os.path.exists(DB_PATH):
        print(f"[reconcile] DB not found at {DB_PATH}")
        return 0

    conn = sqlite3.connect(DB_PATH)
    c = conn.cursor()
    row = c.execute("SELECT id, status, payload FROM objects WHERE object_type='sandbox' AND name=?", (SANDBOX_NAME,)).fetchone()
    if not row:
        print(f"[reconcile] No record found for sandbox: {SANDBOX_NAME}")
        conn.close()
        return 0

    obj_id, status, payload_bytes = row
    payload = bytearray(payload_bytes)

    top_items = parse_protobuf(payload)
    f3_list = [it for it in top_items if it[0] == 3 and it[1] == 2]
    if not f3_list:
        conn.close()
        return 0

    f3 = f3_list[0]
    field3_data = f3[2]
    f3_items = parse_protobuf(field3_data)

    is_error = False
    exit_code = 0
    phase_val = None
    for fnum, wtype, val, s, e in f3_items:
        if fnum == 6 and wtype == 0:
            phase_val = val
            if val == 3: # SANDBOX_PHASE_ERROR
                is_error = True
        elif fnum == 9 and wtype == 0:
            exit_code = val

    needs_repair = (status == 'Error' or is_error or (phase_val == 3) or exit_code != 0)

    if not needs_repair:
        print(f"[reconcile] Sandbox is healthy: status='{status}', phase={phase_val}, exit_code={exit_code}")
        conn.close()
        return 0

    print(f"[reconcile] DEADLOCK DETECTED for '{SANDBOX_NAME}'! status='{status}', phase={phase_val}, exit_code={exit_code}")

    # 緊急バックアップ
    bak_path = f"{DB_PATH}.autorepair.{datetime.now().strftime('%Y%m%d%H%M%S')}"
    shutil.copy2(DB_PATH, bak_path)
    print(f"[reconcile] Created backup: {bak_path}")

    # Protobuf 再構築 (Phase: Ready = enum 2, exit_code 削除)
    cond_data = encode_len_delim(1, b'Ready') + encode_len_delim(2, b'True')
    rebuilt_f3 = bytearray()
    for fnum, wtype, val, s, e in f3_items:
        if fnum == 5:
            rebuilt_f3 += encode_len_delim(5, cond_data)
        elif fnum in (6, 7):
            rebuilt_f3 += encode_tag(fnum, 0) + encode_varint(2) # Ready (enum 2)
        elif fnum == 9:
            pass # 異常 exit_code を除去
        else:
            if wtype == 0:
                rebuilt_f3 += encode_tag(fnum, 0) + encode_varint(val)
            elif wtype == 2:
                rebuilt_f3 += encode_len_delim(fnum, val)

    new_payload = bytearray(payload[:f3[3]]) + encode_len_delim(3, bytes(rebuilt_f3))
    c.execute("UPDATE objects SET payload=?, status='Ready' WHERE object_type='sandbox' AND name=?", (bytes(new_payload), SANDBOX_NAME))
    conn.commit()
    conn.close()
    print(f"[reconcile] Successfully repaired DB payload for '{SANDBOX_NAME}' to Ready")

    # Gateway 再起動
    print("[reconcile] Restarting OpenShell Gateway to reload DB state...")
    os.system("systemctl --user restart nemoclaw-openshell-gateway.service")
    return 1

if __name__ == '__main__':
    reconcile()
