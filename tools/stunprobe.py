#!/usr/bin/env python3
"""Minimal STUN binding probe (RFC 5389 subset) used for M4 evidence.

Usage: python tools/stunprobe.py 123.57.253.208:40000 123.57.253.208:40001
"""
import os
import socket
import struct
import sys

MAGIC = 0x2112A442


def query(server, timeout=1.0, retries=3):
    host, port = server.rsplit(":", 1)
    port = int(port)
    for _ in range(retries):
        txn = os.urandom(12)
        req = struct.pack("!HHI", 0x0001, 0, MAGIC) + txn
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.settimeout(timeout)
        try:
            s.sendto(req, (host, port))
            data, _ = s.recvfrom(1500)
        except socket.timeout:
            s.close()
            continue
        finally:
            try:
                s.close()
            except OSError:
                pass
        if len(data) < 20 or struct.unpack("!I", data[4:8])[0] != MAGIC or data[8:20] != txn:
            continue
        typ = struct.unpack("!H", data[0:2])[0]
        if typ != 0x0101:
            continue
        # walk attributes
        i = 20
        while i + 4 <= len(data):
            at, al = struct.unpack("!HH", data[i:i + 4])
            val = data[i + 4:i + 4 + al]
            if at == 0x0020 and len(val) >= 8 and val[1] == 0x01:
                port_x = struct.unpack("!H", val[2:4])[0] ^ (MAGIC >> 16)
                ip_x = bytes(a ^ b for a, b in zip(val[4:8], struct.pack("!I", MAGIC)))
                return (socket.inet_ntoa(ip_x), port_x), txn.hex(), data
            i += 4 + ((al + 3) & ~3)
    return None, None, None


def main():
    servers = sys.argv[1:] or ["123.57.253.208:40000", "123.57.253.208:40001"]
    seen = []
    for srv in servers:
        addr, txn, raw = query(srv)
        print(f"{srv} -> {addr} (txn {txn}, {len(raw) if raw else 0} bytes)")
        if addr:
            seen.append(addr)
    if len(seen) >= 2:
        same = seen[0] == seen[1]
        print("mapping:", "cone (same mapped address)" if same else "symmetric (port changes per destination)")


if __name__ == "__main__":
    main()
